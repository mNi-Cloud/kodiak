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
	"net/url"
	"strings"
	"time"

	"github.com/bufbuild/connect-go"
	ionscaleclient "github.com/jsiebens/ionscale/pkg/client/ionscale"
	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	api "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1/ionscalev1connect"
	"google.golang.org/protobuf/types/known/durationpb"
)

// ControlServerClient provides a client interface for interacting with the control server
type ControlServerClient struct {
	client api.IonscaleServiceClient
	auth   ionscaleclient.ClientAuth
}

// NewControlServerClient creates a new control server client
func NewControlServerClient(serverURL string, systemAdminKey string, insecureSkipVerify bool) (*ControlServerClient, error) {
	// Parse URL to determine if TLS should be used
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server URL: %w", err)
	}

	var transport http.RoundTripper
	if strings.ToLower(parsedURL.Scheme) == "https" {
		// Use TLS transport for HTTPS
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecureSkipVerify,
			},
		}
	} else {
		// Use plain HTTP transport without TLS
		transport = &http.Transport{}
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	auth, err := ionscaleclient.LoadClientAuth(serverURL, systemAdminKey)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare client auth: %w", err)
	}

	interceptors := connect.WithInterceptors(&authInterceptor{auth: auth})
	client := api.NewIonscaleServiceClient(httpClient, serverURL, interceptors)

	return &ControlServerClient{
		client: client,
		auth:   auth,
	}, nil
}

// authInterceptor adds authentication to requests
type authInterceptor struct {
	auth ionscaleclient.ClientAuth
}

func (i *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.auth != nil {
			token, err := i.auth.GetToken()
			if err != nil {
				return nil, fmt.Errorf("failed to get auth token: %w", err)
			}
			if token != "" {
				req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", token))
			}
		}
		return next(ctx, req)
	}
}

func (i *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		if i.auth != nil {
			token, err := i.auth.GetToken()
			if err == nil && token != "" {
				conn.RequestHeader().Set("Authorization", fmt.Sprintf("Bearer %s", token))
			}
		}
		return conn
	}
}

func (i *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// CreateTailnet creates a new tailnet
func (c *ControlServerClient) CreateTailnet(ctx context.Context, request *pb.CreateTailnetRequest) (*pb.Tailnet, error) {
	resp, err := c.client.CreateTailnet(ctx, connect.NewRequest(request))
	if err != nil {
		return nil, fmt.Errorf("failed to create tailnet %q: %w", request.GetName(), err)
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
		return nil, fmt.Errorf("failed to get tailnet %d: %w", tailnetID, err)
	}

	return resp.Msg.Tailnet, nil
}

// UpdateTailnet updates an existing tailnet
func (c *ControlServerClient) UpdateTailnet(ctx context.Context, request *pb.UpdateTailnetRequest) (*pb.Tailnet, error) {
	resp, err := c.client.UpdateTailnet(ctx, connect.NewRequest(request))
	if err != nil {
		return nil, fmt.Errorf("failed to update tailnet %d: %w", request.GetTailnetId(), err)
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
	if err != nil {
		return fmt.Errorf("failed to delete tailnet %d: %w", tailnetID, err)
	}
	return nil
}

// ListTailnets lists all tailnets
func (c *ControlServerClient) ListTailnets(ctx context.Context) ([]*pb.Tailnet, error) {
	req := connect.NewRequest(&pb.ListTailnetsRequest{})

	resp, err := c.client.ListTailnets(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list tailnets: %w", err)
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
		return nil, "", fmt.Errorf("failed to create auth key for tailnet %d: %w", tailnetID, err)
	}

	return resp.Msg.AuthKey, resp.Msg.Value, nil
}

// DeleteAuthKey deletes an auth key
func (c *ControlServerClient) DeleteAuthKey(ctx context.Context, authKeyID uint64) error {
	req := connect.NewRequest(&pb.DeleteAuthKeyRequest{
		AuthKeyId: authKeyID,
	})

	_, err := c.client.DeleteAuthKey(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete auth key %d: %w", authKeyID, err)
	}
	return nil
}

// ListAuthKeys lists auth keys for a tailnet
func (c *ControlServerClient) ListAuthKeys(ctx context.Context, tailnetID uint64) ([]*pb.AuthKey, error) {
	req := connect.NewRequest(&pb.ListAuthKeysRequest{
		TailnetId: tailnetID,
	})

	resp, err := c.client.ListAuthKeys(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list auth keys for tailnet %d: %w", tailnetID, err)
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
		return nil, fmt.Errorf("failed to list machines for tailnet %d: %w", tailnetID, err)
	}

	return resp.Msg.Machines, nil
}

// EnableMachineRoutes ensures the provided routes are enabled for the machine
func (c *ControlServerClient) EnableMachineRoutes(ctx context.Context, machineID uint64, routes []string, replace bool) (*pb.MachineRoutes, error) {
	req := connect.NewRequest(&pb.EnableMachineRoutesRequest{
		MachineId: machineID,
		Routes:    routes,
		Replace:   replace,
	})

	resp, err := c.client.EnableMachineRoutes(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to enable routes for machine %d: %w", machineID, err)
	}

	return resp.Msg.Routes, nil
}

// DeleteMachine deletes a machine
func (c *ControlServerClient) DeleteMachine(ctx context.Context, machineID uint64) error {
	req := connect.NewRequest(&pb.DeleteMachineRequest{
		MachineId: machineID,
	})

	_, err := c.client.DeleteMachine(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete machine %d: %w", machineID, err)
	}
	return nil
}

// GetVersion gets the control server version
func (c *ControlServerClient) GetVersion(ctx context.Context) (*pb.GetVersionResponse, error) {
	req := connect.NewRequest(&pb.GetVersionRequest{})

	resp, err := c.client.GetVersion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get control server version: %w", err)
	}

	return resp.Msg, nil
}
