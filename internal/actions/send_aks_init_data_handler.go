package actions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/internal/castai"
)

var _ ActionHandler = &SendAKSInitDataHandler{}

func NewSendAKSInitDataHandler(log logrus.FieldLogger, client castai.CastAIClient) *SendAKSInitDataHandler {
	return &SendAKSInitDataHandler{
		log:    log,
		client: client,

		baseDir:         "/var/lib/waagent",
		cloudConfigPath: "/var/lib/waagent/ovf-env.xml",
	}
}

type SendAKSInitDataHandler struct {
	log    logrus.FieldLogger
	client castai.CastAIClient

	baseDir         string
	cloudConfigPath string
}

func (s *SendAKSInitDataHandler) Handle(ctx context.Context, _ *castai.ClusterAction) error {
	cloudConfig, err := s.readCloudConfigBase64(s.cloudConfigPath)
	if err != nil {
		return fmt.Errorf("reading cloud config: %w", err)
	}
	settingsPath, err := s.findSettingsPath(s.baseDir)
	if err != nil {
		return fmt.Errorf("protected settings path: %w", err)
	}
	settings, err := s.readSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("protected settings read: %w", err)
	}
	protectedSettings, err := s.decryptProtectedSettings(settings)
	if err != nil {
		return fmt.Errorf("protected settings decrypt failed: %w", err)
	}
	return s.client.SendAKSInitData(ctx, &castai.AKSInitDataRequest{
		CloudConfigBase64:       string(cloudConfig),
		ProtectedSettingsBase64: base64.StdEncoding.EncodeToString(protectedSettings),
	})
}

var (
	customDataRegex = regexp.MustCompile(`<ns1:CustomData>(.*?)<\/ns1:CustomData>`)
	errNoXML        = errors.New("no custom data xml tag found")
)

// readCloudConfigBase64 extracts base64 encoded cloud config content from XML file.
func (s *SendAKSInitDataHandler) readCloudConfigBase64(cloudConfigPath string) ([]byte, error) {
	xmlContent, err := os.ReadFile(cloudConfigPath)
	if err != nil {
		return nil, err
	}
	matches := customDataRegex.FindSubmatch(xmlContent)
	if len(matches) < 2 {
		return nil, errNoXML
	}
	return matches[1], nil
}

// findSettingsPath searches for custom script settings file path which contains encrypted init data env variables.
func (s *SendAKSInitDataHandler) findSettingsPath(baseDir string) (string, error) {
	var res string
	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if strings.Contains(path, "Microsoft.Azure.Extensions.CustomScript-") && strings.HasSuffix(path, "settings") {
			res = path
			return io.EOF
		}
		return err
	})
	if !errors.Is(err, io.EOF) {
		return "", err
	}
	if res == "" {
		return "", fmt.Errorf("settings path not found, base dir=%s", baseDir)
	}
	return res, nil
}

func (s *SendAKSInitDataHandler) readSettings(settingsFilePath string) (*settings, error) {
	var res settings
	settingsContent, err := os.ReadFile(settingsFilePath)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(settingsContent, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *SendAKSInitDataHandler) decryptProtectedSettings(settings *settings) ([]byte, error) {
	protectedsettings := settings.Runtimesettings[0].Handlersettings.Protectedsettings
	protectedSettingsBytes, err := base64.StdEncoding.DecodeString(protectedsettings)
	if err != nil {
		return nil, err
	}
	thumbprint := settings.Runtimesettings[0].Handlersettings.Protectedsettingscertthumbprint

	args := []string{
		"smime",
		"-decrypt",
		"-binary",
		"-inform", "DEM",
		"-inkey", path.Join(s.baseDir, fmt.Sprintf("%s.prv", thumbprint)),
	}
	cmd := exec.Command(
		"openssl",
		args...,
	)
	cmd.Stdin = bytes.NewBuffer(protectedSettingsBytes)
	var resBuf, errBuf bytes.Buffer
	cmd.Stdout = &resBuf
	cmd.Stderr = &errBuf
	s.log.Debugf("running cmd: openssl %s", strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %w", errBuf.String(), err)
	}
	return resBuf.Bytes(), err
}

type settings struct {
	Runtimesettings []struct {
		Handlersettings struct {
			Publicsettings                  struct{} `json:"publicSettings"`
			Protectedsettings               string   `json:"protectedSettings"`
			Protectedsettingscertthumbprint string   `json:"protectedSettingsCertThumbprint"`
		} `json:"handlerSettings"`
	} `json:"runtimeSettings"`
}
