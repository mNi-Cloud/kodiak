/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package client

import (
	"context"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
)

// MockControlServerClient is a mock implementation of ControlServerClientInterface for testing
type MockControlServerClient struct {
	CreateTailnetFunc func(ctx context.Context, request *pb.CreateTailnetRequest) (*pb.Tailnet, error)
	GetTailnetFunc    func(ctx context.Context, tailnetID uint64) (*pb.Tailnet, error)
	UpdateTailnetFunc func(ctx context.Context, request *pb.UpdateTailnetRequest) (*pb.Tailnet, error)
	DeleteTailnetFunc func(ctx context.Context, tailnetID uint64, force bool) error
	ListTailnetsFunc  func(ctx context.Context) ([]*pb.Tailnet, error)
	CreateAuthKeyFunc func(ctx context.Context, tailnetID uint64, ephemeral bool, expiry time.Duration, tags []string, preAuthorized bool) (*pb.AuthKey, string, error)
	DeleteAuthKeyFunc func(ctx context.Context, authKeyID uint64) error
	ListAuthKeysFunc  func(ctx context.Context, tailnetID uint64) ([]*pb.AuthKey, error)
	ListMachinesFunc  func(ctx context.Context, tailnetID uint64) ([]*pb.Machine, error)
	DeleteMachineFunc func(ctx context.Context, machineID uint64) error
	GetVersionFunc    func(ctx context.Context) (*pb.GetVersionResponse, error)

	// Call tracking
	CreateTailnetCalls []CreateTailnetCall
	GetTailnetCalls    []uint64
	UpdateTailnetCalls []UpdateTailnetCall
	DeleteTailnetCalls []DeleteTailnetCall
	ListTailnetsCalls  int
	CreateAuthKeyCalls []CreateAuthKeyCall
	DeleteAuthKeyCalls []uint64
	ListAuthKeysCalls  []uint64
	ListMachinesCalls  []uint64
	DeleteMachineCalls []uint64
	GetVersionCalls    int
}

// Call tracking structures
type CreateTailnetCall struct {
	Request *pb.CreateTailnetRequest
}

type UpdateTailnetCall struct {
	Request *pb.UpdateTailnetRequest
}

type DeleteTailnetCall struct {
	TailnetID uint64
	Force     bool
}

type CreateAuthKeyCall struct {
	TailnetID     uint64
	Ephemeral     bool
	Expiry        time.Duration
	Tags          []string
	PreAuthorized bool
}

// Ensure MockControlServerClient implements ControlServerClientInterface
var _ ControlServerClientInterface = (*MockControlServerClient)(nil)

func (m *MockControlServerClient) CreateTailnet(ctx context.Context, request *pb.CreateTailnetRequest) (*pb.Tailnet, error) {
	m.CreateTailnetCalls = append(m.CreateTailnetCalls, CreateTailnetCall{Request: request})
	if m.CreateTailnetFunc != nil {
		return m.CreateTailnetFunc(ctx, request)
	}
	return &pb.Tailnet{Id: 1, Name: request.Name}, nil
}

func (m *MockControlServerClient) GetTailnet(ctx context.Context, tailnetID uint64) (*pb.Tailnet, error) {
	m.GetTailnetCalls = append(m.GetTailnetCalls, tailnetID)
	if m.GetTailnetFunc != nil {
		return m.GetTailnetFunc(ctx, tailnetID)
	}
	return &pb.Tailnet{Id: tailnetID}, nil
}

func (m *MockControlServerClient) UpdateTailnet(ctx context.Context, request *pb.UpdateTailnetRequest) (*pb.Tailnet, error) {
	m.UpdateTailnetCalls = append(m.UpdateTailnetCalls, UpdateTailnetCall{Request: request})
	if m.UpdateTailnetFunc != nil {
		return m.UpdateTailnetFunc(ctx, request)
	}
	return &pb.Tailnet{Id: request.TailnetId}, nil
}

func (m *MockControlServerClient) DeleteTailnet(ctx context.Context, tailnetID uint64, force bool) error {
	m.DeleteTailnetCalls = append(m.DeleteTailnetCalls, DeleteTailnetCall{TailnetID: tailnetID, Force: force})
	if m.DeleteTailnetFunc != nil {
		return m.DeleteTailnetFunc(ctx, tailnetID, force)
	}
	return nil
}

func (m *MockControlServerClient) ListTailnets(ctx context.Context) ([]*pb.Tailnet, error) {
	m.ListTailnetsCalls++
	if m.ListTailnetsFunc != nil {
		return m.ListTailnetsFunc(ctx)
	}
	return []*pb.Tailnet{}, nil
}

func (m *MockControlServerClient) CreateAuthKey(ctx context.Context, tailnetID uint64, ephemeral bool, expiry time.Duration, tags []string, preAuthorized bool) (*pb.AuthKey, string, error) {
	m.CreateAuthKeyCalls = append(m.CreateAuthKeyCalls, CreateAuthKeyCall{
		TailnetID:     tailnetID,
		Ephemeral:     ephemeral,
		Expiry:        expiry,
		Tags:          tags,
		PreAuthorized: preAuthorized,
	})
	if m.CreateAuthKeyFunc != nil {
		return m.CreateAuthKeyFunc(ctx, tailnetID, ephemeral, expiry, tags, preAuthorized)
	}
	return &pb.AuthKey{Id: 1}, "test-auth-key-value", nil
}

func (m *MockControlServerClient) DeleteAuthKey(ctx context.Context, authKeyID uint64) error {
	m.DeleteAuthKeyCalls = append(m.DeleteAuthKeyCalls, authKeyID)
	if m.DeleteAuthKeyFunc != nil {
		return m.DeleteAuthKeyFunc(ctx, authKeyID)
	}
	return nil
}

func (m *MockControlServerClient) ListAuthKeys(ctx context.Context, tailnetID uint64) ([]*pb.AuthKey, error) {
	m.ListAuthKeysCalls = append(m.ListAuthKeysCalls, tailnetID)
	if m.ListAuthKeysFunc != nil {
		return m.ListAuthKeysFunc(ctx, tailnetID)
	}
	return []*pb.AuthKey{}, nil
}

func (m *MockControlServerClient) ListMachines(ctx context.Context, tailnetID uint64) ([]*pb.Machine, error) {
	m.ListMachinesCalls = append(m.ListMachinesCalls, tailnetID)
	if m.ListMachinesFunc != nil {
		return m.ListMachinesFunc(ctx, tailnetID)
	}
	return []*pb.Machine{}, nil
}

func (m *MockControlServerClient) DeleteMachine(ctx context.Context, machineID uint64) error {
	m.DeleteMachineCalls = append(m.DeleteMachineCalls, machineID)
	if m.DeleteMachineFunc != nil {
		return m.DeleteMachineFunc(ctx, machineID)
	}
	return nil
}

func (m *MockControlServerClient) GetVersion(ctx context.Context) (*pb.GetVersionResponse, error) {
	m.GetVersionCalls++
	if m.GetVersionFunc != nil {
		return m.GetVersionFunc(ctx)
	}
	return &pb.GetVersionResponse{Version: "test-version"}, nil
}

// Reset clears all call tracking
func (m *MockControlServerClient) Reset() {
	m.CreateTailnetCalls = nil
	m.GetTailnetCalls = nil
	m.UpdateTailnetCalls = nil
	m.DeleteTailnetCalls = nil
	m.ListTailnetsCalls = 0
	m.CreateAuthKeyCalls = nil
	m.DeleteAuthKeyCalls = nil
	m.ListAuthKeysCalls = nil
	m.ListMachinesCalls = nil
	m.DeleteMachineCalls = nil
	m.GetVersionCalls = 0
}

// NewMockClientFactory returns a ClientFactory that always returns the provided mock client
func NewMockClientFactory(mock *MockControlServerClient) ClientFactory {
	return func(serverURL string, systemAdminKey string, insecureSkipVerify bool) (ControlServerClientInterface, error) {
		return mock, nil
	}
}
