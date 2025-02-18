// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/eventlogger"
	"github.com/hashicorp/go-bexpr"
	"github.com/hashicorp/vault/helper/namespace"
	"github.com/hashicorp/vault/internal/observability/event"
	"github.com/hashicorp/vault/sdk/logical"
)

var _ eventlogger.Node = (*EntryFilter)(nil)

// NewEntryFilter should be used to create an EntryFilter node.
// The filter supplied should be in bexpr format and reference fields from logical.LogInputBexpr.
func NewEntryFilter(filter string) (*EntryFilter, error) {
	const op = "audit.NewEntryFilter"

	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, fmt.Errorf("%s: cannot create new audit filter with empty filter expression: %w", op, event.ErrInvalidParameter)
	}

	eval, err := bexpr.CreateEvaluator(filter)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot create new audit filter: %w", op, err)
	}

	// Validate the filter by attempting to evaluate it with an empty input.
	// This prevents users providing a filter with a field that would error during
	// matching, and block all auditable requests to Vault.
	li := logical.LogInputBexpr{}
	_, err = eval.Evaluate(li)
	if err != nil {
		return nil, fmt.Errorf("%s: filter references an unsupported field: %s", op, filter)
	}

	return &EntryFilter{evaluator: eval}, nil
}

// Reopen is a no-op for the filter node.
func (*EntryFilter) Reopen() error {
	return nil
}

// Type describes the type of this node (filter).
func (*EntryFilter) Type() eventlogger.NodeType {
	return eventlogger.NodeTypeFilter
}

// Process will attempt to parse the incoming event data and decide whether it
// should be filtered or remain in the pipeline and passed to the next node.
func (f *EntryFilter) Process(ctx context.Context, e *eventlogger.Event) (*eventlogger.Event, error) {
	const op = "audit.(EntryFilter).Process"

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if e == nil {
		return nil, fmt.Errorf("%s: event is nil: %w", op, event.ErrInvalidParameter)
	}

	a, ok := e.Payload.(*AuditEvent)
	if !ok {
		return nil, fmt.Errorf("%s: cannot parse event payload: %w", op, event.ErrInvalidParameter)
	}

	// If we don't have data to process, then we're done.
	if a.Data == nil {
		return nil, nil
	}

	ns, err := namespace.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot obtain namespace: %w", op, err)
	}

	datum := a.Data.BexprDatum(ns.Path)

	result, err := f.evaluator.Evaluate(datum)
	if err != nil {
		return nil, fmt.Errorf("%s: unable to evaluate filter: %w", op, err)
	}

	if result {
		// Allow this event to carry on through the pipeline.
		return e, nil
	}

	// End process of this pipeline.
	return nil, nil
}
