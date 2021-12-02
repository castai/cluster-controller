// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/castai/cluster-controller/helm (interfaces: Client)

// Package mock_helm is a generated GoMock package.
package mock_helm

import (
	context "context"
	reflect "reflect"

	helm "github.com/castai/cluster-controller/helm"
	gomock "github.com/golang/mock/gomock"
	release "helm.sh/helm/v3/pkg/release"
)

// MockClient is a mock of Client interface.
type MockClient struct {
	ctrl     *gomock.Controller
	recorder *MockClientMockRecorder
}

// MockClientMockRecorder is the mock recorder for MockClient.
type MockClientMockRecorder struct {
	mock *MockClient
}

// NewMockClient creates a new mock instance.
func NewMockClient(ctrl *gomock.Controller) *MockClient {
	mock := &MockClient{ctrl: ctrl}
	mock.recorder = &MockClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockClient) EXPECT() *MockClientMockRecorder {
	return m.recorder
}

// GetRelease mocks base method.
func (m *MockClient) GetRelease(arg0 helm.GetReleaseOptions) (*release.Release, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetRelease", arg0)
	ret0, _ := ret[0].(*release.Release)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetRelease indicates an expected call of GetRelease.
func (mr *MockClientMockRecorder) GetRelease(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetRelease", reflect.TypeOf((*MockClient)(nil).GetRelease), arg0)
}

// Install mocks base method.
func (m *MockClient) Install(arg0 context.Context, arg1 helm.InstallOptions) (*release.Release, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Install", arg0, arg1)
	ret0, _ := ret[0].(*release.Release)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Install indicates an expected call of Install.
func (mr *MockClientMockRecorder) Install(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Install", reflect.TypeOf((*MockClient)(nil).Install), arg0, arg1)
}

// Upgrade mocks base method.
func (m *MockClient) Upgrade(arg0 context.Context, arg1 helm.UpgradeOptions) (*release.Release, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Upgrade", arg0, arg1)
	ret0, _ := ret[0].(*release.Release)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Upgrade indicates an expected call of Upgrade.
func (mr *MockClientMockRecorder) Upgrade(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Upgrade", reflect.TypeOf((*MockClient)(nil).Upgrade), arg0, arg1)
}
