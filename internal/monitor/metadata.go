package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type Metadata struct {
	ClusterID string `json:"clusterId"`
	LastStart int64  `json:"lastStart"`
}

func (m *Metadata) Save(file string) error {
	if file == "" {
		// if monitor is running standalone or with an old chart version, and saving of
		// metadata is not configured, we don't need to do anything here
		return nil
	}
	contents, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshaling: %w", err)
	}
	return os.WriteFile(file, contents, 0o600)
}

var errEmptyMetadata = fmt.Errorf("metadata file is empty")

func (m *Metadata) Load(file string) error {
	contents, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}
	if len(contents) == 0 {
		return errEmptyMetadata
	}
	if err := json.Unmarshal(contents, m); err != nil {
		return fmt.Errorf("file: %v content: %v parsing json: %w", file, string(contents), err)
	}
	return nil
}

// watchForMetadataChanges starts a watch on a local file for updates and returns changes to metadata channel. watcher stops when context is done
func watchForMetadataChanges(ctx context.Context, log logrus.FieldLogger, metadataFilePath string) (chan Metadata, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("setting up new watcher: %w", err)
	}
	updates := make(chan Metadata, 1)

	if err := watcher.Add(filepath.Dir(metadataFilePath)); err != nil {
		return nil, fmt.Errorf("adding watch: %w", err)
	}

	checkMetadata := func() {
		metadata := Metadata{}
		if err := metadata.Load(metadataFilePath); err != nil {
			if !strings.Contains(err.Error(), "no such file or directory") {
				log.Warnf("loading metadata failed: %v", err)
			}
		} else {
			select {
			case updates <- metadata:
			default:
				log.Warnf("metadata update skipped, channel full")
			}
		}
	}

	go func() {
		defer close(updates)
		defer func() {
			err := watcher.Close()
			if err != nil {
				log.Warnf("watcher close error: %v", err)
			}
		}()
		checkMetadata()

		for {
			select {
			case <-ctx.Done():
				return
			case event := <-watcher.Events:
				if opContains(event.Op, fsnotify.Create, fsnotify.Write) && event.Name == metadataFilePath {
					checkMetadata()
				}
			case err := <-watcher.Errors:
				log.Errorf("metadata watch error: %v", err)
			}
		}
	}()

	return updates, nil
}

// opContains tests that op contains at least one of the values
func opContains(op fsnotify.Op, values ...fsnotify.Op) bool {
	for _, v := range values {
		// event.Op may contain multiple values or-ed together, can't use simple equality check
		if op&v == v {
			return true
		}
	}
	return false
}
