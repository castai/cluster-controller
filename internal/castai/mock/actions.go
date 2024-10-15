// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/castai/cluster-controller/internal/castai (interfaces: CastAIClient)

// Package mock_castai is a generated GoMock package.
package mock_castai

import (
	context "context"
	reflect "reflect"

	types "github.com/castai/cluster-controller/internal/types"
	gomock "github.com/golang/mock/gomock"
)

// MockCastAIClient is a mock of CastAIClient interface.
type MockCastAIClient struct {
	ctrl     *gomock.Controller
	recorder *MockCastAIClientMockRecorder
}

// MockCastAIClientMockRecorder is the mock recorder for MockCastAIClient.
type MockCastAIClientMockRecorder struct {
	mock *MockCastAIClient
}

// NewMockCastAIClient creates a new mock instance.
func NewMockCastAIClient(ctrl *gomock.Controller) *MockCastAIClient {
	mock := &MockCastAIClient{ctrl: ctrl}
	mock.recorder = &MockCastAIClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockCastAIClient) EXPECT() *MockCastAIClientMockRecorder {
	return m.recorder
}

// AckAction mocks base method.
func (m *MockCastAIClient) AckAction(arg0 context.Context, arg1 string, arg2 *types.AckClusterActionRequest) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AckAction", arg0, arg1, arg2)
	ret0, _ := ret[0].(error)
	return ret0
}

// AckAction indicates an expected call of AckAction.
func (mr *MockCastAIClientMockRecorder) AckAction(arg0, arg1, arg2 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AckAction", reflect.TypeOf((*MockCastAIClient)(nil).AckAction), arg0, arg1, arg2)
}

// GetActions mocks base method.
func (m *MockCastAIClient) GetActions(arg0 context.Context, arg1 string) ([]*types.ClusterAction, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetActions", arg0, arg1)
	ret0, _ := ret[0].([]*types.ClusterAction)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetActions indicates an expected call of GetActions.
func (mr *MockCastAIClientMockRecorder) GetActions(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetActions", reflect.TypeOf((*MockCastAIClient)(nil).GetActions), arg0, arg1)
}

// SendAKSInitData mocks base method.
func (m *MockCastAIClient) SendAKSInitData(arg0 context.Context, arg1 *types.AKSInitDataRequest) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SendAKSInitData", arg0, arg1)
	ret0, _ := ret[0].(error)
	return ret0
}

// SendAKSInitData indicates an expected call of SendAKSInitData.
func (mr *MockCastAIClientMockRecorder) SendAKSInitData(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SendAKSInitData", reflect.TypeOf((*MockCastAIClient)(nil).SendAKSInitData), arg0, arg1)
}
