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

// ControlServerClientInterface defines the interface for interacting with the control server.
// This interface allows for mocking in unit tests.
type ControlServerClientInterface interface {
	// CreateTailnet creates a new tailnet
	CreateTailnet(ctx context.Context, request *pb.CreateTailnetRequest) (*pb.Tailnet, error)

	// GetTailnet retrieves a tailnet by ID
	GetTailnet(ctx context.Context, tailnetID uint64) (*pb.Tailnet, error)

	// UpdateTailnet updates an existing tailnet
	UpdateTailnet(ctx context.Context, request *pb.UpdateTailnetRequest) (*pb.Tailnet, error)

	// DeleteTailnet deletes a tailnet
	DeleteTailnet(ctx context.Context, tailnetID uint64, force bool) error

	// ListTailnets lists all tailnets
	ListTailnets(ctx context.Context) ([]*pb.Tailnet, error)

	// CreateAuthKey creates a new auth key
	CreateAuthKey(ctx context.Context, tailnetID uint64, ephemeral bool, expiry time.Duration, tags []string, preAuthorized bool) (*pb.AuthKey, string, error)

	// DeleteAuthKey deletes an auth key
	DeleteAuthKey(ctx context.Context, authKeyID uint64) error

	// ListAuthKeys lists auth keys for a tailnet
	ListAuthKeys(ctx context.Context, tailnetID uint64) ([]*pb.AuthKey, error)

	// ListMachines lists machines in a tailnet
	ListMachines(ctx context.Context, tailnetID uint64) ([]*pb.Machine, error)

	// EnableMachineRoutes ensures the provided routes are enabled for the machine
	EnableMachineRoutes(ctx context.Context, machineID uint64, routes []string, replace bool) (*pb.MachineRoutes, error)

	// DeleteMachine deletes a machine
	DeleteMachine(ctx context.Context, machineID uint64) error

	// GetVersion gets the control server version
	GetVersion(ctx context.Context) (*pb.GetVersionResponse, error)
}

// Ensure ControlServerClient implements ControlServerClientInterface
var _ ControlServerClientInterface = (*ControlServerClient)(nil)

// ClientFactory is a function type for creating ControlServerClientInterface instances
type ClientFactory func(serverURL string, systemAdminKey string, insecureSkipVerify bool) (ControlServerClientInterface, error)

// DefaultClientFactory returns the default client factory that creates real ControlServerClient instances
func DefaultClientFactory() ClientFactory {
	return func(serverURL string, systemAdminKey string, insecureSkipVerify bool) (ControlServerClientInterface, error) {
		return NewControlServerClient(serverURL, systemAdminKey, insecureSkipVerify)
	}
}
