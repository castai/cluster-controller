package castai

import "github.com/castai/cluster-controller/types"

type getClusterActionsResponse struct {
	Items []*types.ClusterAction `json:"items"`
}

type ackClusterActionRequest struct {
	Error *string `json:"error"`
}

type aksInitDataRequest struct {
	CloudConfigBase64       string `json:"cloudConfigBase64"`
	ProtectedSettingsBase64 string `json:"protectedSettingsBase64"`
	Architecture            string `json:"architecture"`
}
