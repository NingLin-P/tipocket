// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package core

import (
	"context"
)

// UnknownResponse means we don't know weather this operation
// succeeds or not.
type UnknownResponse interface {
	IsUnknown() bool
}

// Client applies the request to the database.
// Client is used in control.
// You should define your own client for your database.
type Client interface {
	// SetUp sets up the client.
	SetUp(ctx context.Context, nodes []string, node string) error
	// TearDown tears down the client.
	TearDown(ctx context.Context, nodes []string, node string) error
	// Invoke invokes a request to the database.
	// Mostly, the return Response should implement UnknownResponse interface
	Invoke(ctx context.Context, node string, r interface{}) interface{}
	// NextRequest generates a request for latter Invoke.
	NextRequest() interface{}
	// DumpState the database state(also the model's state)
	DumpState(ctx context.Context) (interface{}, error)
}

// ClientCreator creates a client.
// The control will create one client for one node.
type ClientCreator interface {
	// Create creates the client.
	Create(node string) Client
}

// NoopClientCreator creates a noop client.
type NoopClientCreator struct {
}

// Create creates the client.
func (NoopClientCreator) Create(node string) Client {
	return noopClient{}
}

// noopClient is a noop client
type noopClient struct {
}

// SetUp sets up the client.
func (noopClient) SetUp(ctx context.Context, nodes []string, node string) error { return nil }

// TearDown tears down the client.
func (noopClient) TearDown(ctx context.Context, nodes []string, node string) error { return nil }

// Invoke invokes a request to the database.
func (noopClient) Invoke(ctx context.Context, node string, r interface{}) interface{} {
	return nil
}

// NextRequest generates a request for latter Invoke.
func (noopClient) NextRequest() interface{} {
	return nil
}

// DumpState the database state(also the model's state)
func (noopClient) DumpState(ctx context.Context) (interface{}, error) {
	return nil, nil
}