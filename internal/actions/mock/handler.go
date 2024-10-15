// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/castai/cluster-controller/internal/actions (interfaces: ActionHandler)

// Package mock_actions is a generated GoMock package.
package mock_actions

import (
	context "context"
	reflect "reflect"

	castai "github.com/castai/cluster-controller/internal/castai"
	gomock "github.com/golang/mock/gomock"
)

// MockActionHandler is a mock of ActionHandler interface.
type MockActionHandler struct {
	ctrl     *gomock.Controller
	recorder *MockActionHandlerMockRecorder
}

// MockActionHandlerMockRecorder is the mock recorder for MockActionHandler.
type MockActionHandlerMockRecorder struct {
	mock *MockActionHandler
}

// NewMockActionHandler creates a new mock instance.
func NewMockActionHandler(ctrl *gomock.Controller) *MockActionHandler {
	mock := &MockActionHandler{ctrl: ctrl}
	mock.recorder = &MockActionHandlerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockActionHandler) EXPECT() *MockActionHandlerMockRecorder {
	return m.recorder
}

// Handle mocks base method.
func (m *MockActionHandler) Handle(arg0 context.Context, arg1 *castai.ClusterAction) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Handle", arg0, arg1)
	ret0, _ := ret[0].(error)
	return ret0
}

// Handle indicates an expected call of Handle.
func (mr *MockActionHandlerMockRecorder) Handle(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Handle", reflect.TypeOf((*MockActionHandler)(nil).Handle), arg0, arg1)
}