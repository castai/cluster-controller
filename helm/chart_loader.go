//go:generate mockgen -destination ./mock/chart_loader.go . ChartLoader

package helm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/castai/cluster-controller/castai"
	"github.com/cenkalti/backoff/v4"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
)

type ChartLoader interface {
	Load(ctx context.Context, c *castai.ChartSource) (*chart.Chart, error)
}

func NewChartLoader() ChartLoader {
	return &remoteChartLoader{}
}

// remoteChartLoader fetches chart from remote source by given url.
type remoteChartLoader struct {
}

func (cl *remoteChartLoader) Load(ctx context.Context, c *castai.ChartSource) (*chart.Chart, error) {
	var res *chart.Chart
	err := backoff.Retry(func() error {
		index, err := cl.downloadHelmIndex(c.RepoURL)
		if err != nil {
			return err
		}

		urls, err := cl.chartURLs(index, c.Name, c.Version)
		if err != nil {
			return err
		}

		var archiveResp *http.Response
		for _, u := range urls {
			archiveResp, err = cl.fetchArchive(ctx, u)
			if err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
		defer archiveResp.Body.Close()

		ch, err := loader.LoadArchive(archiveResp.Body)
		if err != nil {
			return fmt.Errorf("loading chart from archive: %w", err)
		}
		res = ch
		return nil
	}, defaultBackoff(ctx))
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (cl *remoteChartLoader) fetchArchive(ctx context.Context, archiveURL string) (*http.Response, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	archiveReq, err := http.NewRequestWithContext(ctx, "GET", archiveURL, nil)
	if err != nil {
		return nil, err
	}
	archiveReq.Header.Add("Accept", "application/octet-stream")
	archiveResp, err := httpClient.Do(archiveReq)
	if err != nil {
		return nil, err
	}
	if archiveResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expected archive %s fetch status %d, got %d", archiveURL, http.StatusOK, archiveResp.StatusCode)
	}
	return archiveResp, nil
}

func defaultBackoff(ctx context.Context) backoff.BackOffContext {
	return backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Second), 5), ctx)
}

func (cl *remoteChartLoader) downloadHelmIndex(repoURL string) (*repo.IndexFile, error) {
	r, err := repo.NewChartRepository(&repo.Entry{URL: repoURL}, getter.All(&cli.EnvSettings{}))
	if err != nil {
		return nil, fmt.Errorf("initializing chart repo %s: %w", repoURL, err)
	}

	indexFilepath, err := r.DownloadIndexFile()
	if err != nil {
		return nil, fmt.Errorf("downloading index file: %w", err)
	}

	index, err := repo.LoadIndexFile(indexFilepath)
	if err != nil {
		return nil, fmt.Errorf("reading downloaded index file: %w", err)
	}

	return index, nil
}

func (cl *remoteChartLoader) chartURLs(index *repo.IndexFile, name, version string) ([]string, error) {
	for _, c := range index.Entries[name] {
		if c.Version == version && len(c.URLs) > 0 {
			return c.URLs, nil
		}
	}

	return nil, fmt.Errorf("finding chart %q version %q in helm repo index", name, version)
}
