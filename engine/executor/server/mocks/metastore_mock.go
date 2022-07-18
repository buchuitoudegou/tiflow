// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/pingcap/tiflow/engine/executor/server (interfaces: MetastoreCreator)

// Package mocks is a generated GoMock package.
package mocks

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	model "github.com/pingcap/tiflow/engine/pkg/meta/model"
	orm "github.com/pingcap/tiflow/engine/pkg/orm"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// MockMetastoreCreator is a mock of MetastoreCreator interface.
type MockMetastoreCreator struct {
	ctrl     *gomock.Controller
	recorder *MockMetastoreCreatorMockRecorder
}

// MockMetastoreCreatorMockRecorder is the mock recorder for MockMetastoreCreator.
type MockMetastoreCreatorMockRecorder struct {
	mock *MockMetastoreCreator
}

// NewMockMetastoreCreator creates a new mock instance.
func NewMockMetastoreCreator(ctrl *gomock.Controller) *MockMetastoreCreator {
	mock := &MockMetastoreCreator{ctrl: ctrl}
	mock.recorder = &MockMetastoreCreatorMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockMetastoreCreator) EXPECT() *MockMetastoreCreatorMockRecorder {
	return m.recorder
}

// CreateClientConnForBusiness mocks base method.
func (m *MockMetastoreCreator) CreateClientConnForBusiness(arg0 context.Context, arg1 model.StoreConfig) (model.ClientConn, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateClientConnForBusiness", arg0, arg1)
	ret0, _ := ret[0].(model.ClientConn)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateClientConnForBusiness indicates an expected call of CreateClientConnForBusiness.
func (mr *MockMetastoreCreatorMockRecorder) CreateClientConnForBusiness(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateClientConnForBusiness", reflect.TypeOf((*MockMetastoreCreator)(nil).CreateClientConnForBusiness), arg0, arg1)
}

// CreateDBClientForFramework mocks base method.
func (m *MockMetastoreCreator) CreateDBClientForFramework(arg0 context.Context, arg1 model.StoreConfig) (orm.Client, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateDBClientForFramework", arg0, arg1)
	ret0, _ := ret[0].(orm.Client)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateDBClientForFramework indicates an expected call of CreateDBClientForFramework.
func (mr *MockMetastoreCreatorMockRecorder) CreateDBClientForFramework(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateDBClientForFramework", reflect.TypeOf((*MockMetastoreCreator)(nil).CreateDBClientForFramework), arg0, arg1)
}

// CreateEtcdCliForServiceDiscovery mocks base method.
func (m *MockMetastoreCreator) CreateEtcdCliForServiceDiscovery(arg0 context.Context, arg1 model.StoreConfig) (*clientv3.Client, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateEtcdCliForServiceDiscovery", arg0, arg1)
	ret0, _ := ret[0].(*clientv3.Client)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateEtcdCliForServiceDiscovery indicates an expected call of CreateEtcdCliForServiceDiscovery.
func (mr *MockMetastoreCreatorMockRecorder) CreateEtcdCliForServiceDiscovery(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateEtcdCliForServiceDiscovery", reflect.TypeOf((*MockMetastoreCreator)(nil).CreateEtcdCliForServiceDiscovery), arg0, arg1)
}
