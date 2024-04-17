package helm

import "errors"

type ChartSource struct {
	RepoURL string `json:"repoUrl"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (c *ChartSource) Validate() error {
	if c.Name == "" {
		return errors.New("chart name is not set")
	}
	if c.RepoURL == "" {
		return errors.New("chart repoURL is not set")
	}
	if c.Version == "" {
		return errors.New("chart version is not set")
	}
	return nil
}
