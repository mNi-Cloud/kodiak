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
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/bufbuild/connect-go"
	api "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1/ionscalev1connect"
	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// ControlServerClient provides a client interface for interacting with the control server
type ControlServerClient struct {
	client api.IonscaleServiceClient
	token  string
}

// NewControlServerClient creates a new control server client
func NewControlServerClient(serverURL string, token string, insecureSkipVerify bool) (*ControlServerClient, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: insecureSkipVerify,
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 30 * time.Second,
	}

	interceptors := connect.WithInterceptors(&authInterceptor{token: token})
	client := api.NewIonscaleServiceClient(httpClient, serverURL, interceptors)

	return &ControlServerClient{
		client: client,
		token:  token,
	}, nil
}

// authInterceptor adds authentication to requests
type authInterceptor struct {
	token string
}

func (i *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.token != "" {
			req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", i.token))
		}
		return next(ctx, req)
	}
}

func (i *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		if i.token != "" {
			conn.RequestHeader().Set("Authorization", fmt.Sprintf("Bearer %s", i.token))
		}
		return conn
	}
}

func (i *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// CreateTailnet creates a new tailnet
func (c *ControlServerClient) CreateTailnet(ctx context.Context, name string, iamPolicy string, aclPolicy string) (*pb.Tailnet, error) {
	req := connect.NewRequest(&pb.CreateTailnetRequest{
		Name:      name,
		IamPolicy: iamPolicy,
		AclPolicy: aclPolicy,
	})

	resp, err := c.client.CreateTailnet(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Tailnet, nil
}

// GetTailnet retrieves a tailnet by ID
func (c *ControlServerClient) GetTailnet(ctx context.Context, tailnetID uint64) (*pb.Tailnet, error) {
	req := connect.NewRequest(&pb.GetTailnetRequest{
		Id: tailnetID,
	})

	resp, err := c.client.GetTailnet(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Tailnet, nil
}

// UpdateTailnet updates an existing tailnet
func (c *ControlServerClient) UpdateTailnet(ctx context.Context, tailnetID uint64, iamPolicy string, aclPolicy string) (*pb.Tailnet, error) {
	req := connect.NewRequest(&pb.UpdateTailnetRequest{
		TailnetId: tailnetID,
		IamPolicy: iamPolicy,
		AclPolicy: aclPolicy,
	})

	resp, err := c.client.UpdateTailnet(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Tailnet, nil
}

// DeleteTailnet deletes a tailnet
func (c *ControlServerClient) DeleteTailnet(ctx context.Context, tailnetID uint64, force bool) error {
	req := connect.NewRequest(&pb.DeleteTailnetRequest{
		TailnetId: tailnetID,
		Force:     force,
	})

	_, err := c.client.DeleteTailnet(ctx, req)
	return err
}

// ListTailnets lists all tailnets
func (c *ControlServerClient) ListTailnets(ctx context.Context) ([]*pb.Tailnet, error) {
	req := connect.NewRequest(&pb.ListTailnetsRequest{})

	resp, err := c.client.ListTailnets(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Tailnet, nil
}

// CreateAuthKey creates a new auth key
func (c *ControlServerClient) CreateAuthKey(ctx context.Context, tailnetID uint64, ephemeral bool, expiry time.Duration, tags []string, preAuthorized bool) (*pb.AuthKey, string, error) {
	req := connect.NewRequest(&pb.CreateAuthKeyRequest{
		TailnetId:     tailnetID,
		Ephemeral:     ephemeral,
		Tags:          tags,
		PreAuthorized: preAuthorized,
	})

	if expiry > 0 {
		req.Msg.Expiry = durationpb.New(expiry)
	}

	resp, err := c.client.CreateAuthKey(ctx, req)
	if err != nil {
		return nil, "", err
	}

	return resp.Msg.AuthKey, resp.Msg.Value, nil
}

// DeleteAuthKey deletes an auth key
func (c *ControlServerClient) DeleteAuthKey(ctx context.Context, authKeyID uint64) error {
	req := connect.NewRequest(&pb.DeleteAuthKeyRequest{
		AuthKeyId: authKeyID,
	})

	_, err := c.client.DeleteAuthKey(ctx, req)
	return err
}

// ListAuthKeys lists auth keys for a tailnet
func (c *ControlServerClient) ListAuthKeys(ctx context.Context, tailnetID uint64) ([]*pb.AuthKey, error) {
	req := connect.NewRequest(&pb.ListAuthKeysRequest{
		TailnetId: tailnetID,
	})

	resp, err := c.client.ListAuthKeys(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.AuthKeys, nil
}

// ListMachines lists machines in a tailnet
func (c *ControlServerClient) ListMachines(ctx context.Context, tailnetID uint64) ([]*pb.Machine, error) {
	req := connect.NewRequest(&pb.ListMachinesRequest{
		TailnetId: tailnetID,
	})

	resp, err := c.client.ListMachines(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Machines, nil
}

// DeleteMachine deletes a machine
func (c *ControlServerClient) DeleteMachine(ctx context.Context, machineID uint64) error {
	req := connect.NewRequest(&pb.DeleteMachineRequest{
		MachineId: machineID,
	})

	_, err := c.client.DeleteMachine(ctx, req)
	return err
}

// GetVersion gets the control server version
func (c *ControlServerClient) GetVersion(ctx context.Context) (*pb.GetVersionResponse, error) {
	req := connect.NewRequest(&pb.GetVersionRequest{})

	resp, err := c.client.GetVersion(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg, nil
}