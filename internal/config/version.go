package config

import "fmt"

type ClusterControllerVersion struct {
	GitCommit, GitRef, Version string
}

func (a *ClusterControllerVersion) String() string {
	return fmt.Sprintf("GitCommit=%q GitRef=%q Version=%q", a.GitCommit, a.GitRef, a.Version)
}
