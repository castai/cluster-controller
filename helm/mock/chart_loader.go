// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/castai/cluster-controller/helm (interfaces: ChartLoader)

// Package mock_helm is a generated GoMock package.
package mock_helm

import (
	context "context"
	reflect "reflect"

	types "github.com/castai/cluster-controller/types"
	gomock "github.com/golang/mock/gomock"
	chart "helm.sh/helm/v3/pkg/chart"
)

// MockChartLoader is a mock of ChartLoader interface.
type MockChartLoader struct {
	ctrl     *gomock.Controller
	recorder *MockChartLoaderMockRecorder
}

// MockChartLoaderMockRecorder is the mock recorder for MockChartLoader.
type MockChartLoaderMockRecorder struct {
	mock *MockChartLoader
}

// NewMockChartLoader creates a new mock instance.
func NewMockChartLoader(ctrl *gomock.Controller) *MockChartLoader {
	mock := &MockChartLoader{ctrl: ctrl}
	mock.recorder = &MockChartLoaderMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockChartLoader) EXPECT() *MockChartLoaderMockRecorder {
	return m.recorder
}

// Load mocks base method.
func (m *MockChartLoader) Load(arg0 context.Context, arg1 *types.ChartSource) (*chart.Chart, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Load", arg0, arg1)
	ret0, _ := ret[0].(*chart.Chart)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Load indicates an expected call of Load.
func (mr *MockChartLoaderMockRecorder) Load(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Load", reflect.TypeOf((*MockChartLoader)(nil).Load), arg0, arg1)
}
