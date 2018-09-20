package terraform

import (
	"fmt"

	"github.com/hashicorp/terraform/addrs"
	"github.com/hashicorp/terraform/plans"
	"github.com/hashicorp/terraform/providers"
	"github.com/hashicorp/terraform/states"
)

// graphNodeDeposedResource is the graph vertex representing a deposed resource
// instance. Its name is historical: it actually represents a specific resource
// instance, rather than a whole resource.
type graphNodeDeposedResource struct {
	*NodeAbstractResourceInstance
	DeposedKey states.DeposedKey
}

var (
	_ GraphNodeResource            = (*graphNodeDeposedResource)(nil)
	_ GraphNodeResourceInstance    = (*graphNodeDeposedResource)(nil)
	_ GraphNodeDestroyer           = (*graphNodeDeposedResource)(nil)
	_ GraphNodeDestroyerCBD        = (*graphNodeDeposedResource)(nil)
	_ GraphNodeReferenceable       = (*graphNodeDeposedResource)(nil)
	_ GraphNodeReferencer          = (*graphNodeDeposedResource)(nil)
	_ GraphNodeEvalable            = (*graphNodeDeposedResource)(nil)
	_ GraphNodeProviderConsumer    = (*graphNodeDeposedResource)(nil)
	_ GraphNodeProvisionerConsumer = (*graphNodeDeposedResource)(nil)
)

func (n *graphNodeDeposedResource) Name() string {
	return fmt.Sprintf("%s (deposed %s)", n.Addr.String(), n.DeposedKey)
}

// GraphNodeReferenceable implementation
func (n *graphNodeDeposedResource) ReferenceableAddrs() []addrs.Referenceable {
	// Deposed objects don't participate in references.
	return nil
}

// GraphNodeReferencer implementation
func (n *graphNodeDeposedResource) References() []*addrs.Reference {
	// We don't evaluate configuration for deposed objects, so they effectively
	// make no references.
	return nil
}

// GraphNodeDestroyer
func (n *graphNodeDeposedResource) DestroyAddr() *addrs.AbsResourceInstance {
	addr := n.ResourceInstanceAddr()
	return &addr
}

// GraphNodeDestroyerCBD
func (n *graphNodeDeposedResource) CreateBeforeDestroy() bool {
	// A deposed instance is always CreateBeforeDestroy by definition, since
	// we use deposed only to handle create-before-destroy.
	return true
}

// GraphNodeDestroyerCBD
func (n *graphNodeDeposedResource) ModifyCreateBeforeDestroy(v bool) error {
	if !v {
		// Should never happen: deposed instances are _always_ create_before_destroy.
		return fmt.Errorf("can't deactivate create_before_destroy for a deposed instance")
	}
	return nil
}

// GraphNodeEvalable impl.
func (n *graphNodeDeposedResource) EvalTree() EvalNode {
	addr := n.ResourceInstanceAddr()

	var provider providers.Interface
	var providerSchema *ProviderSchema
	var state *states.ResourceInstanceObject

	seq := &EvalSequence{Nodes: make([]EvalNode, 0, 5)}

	// Refresh the resource
	seq.Nodes = append(seq.Nodes, &EvalOpFilter{
		Ops: []walkOperation{walkRefresh},
		Node: &EvalSequence{
			Nodes: []EvalNode{
				&EvalGetProvider{
					Addr:   n.ResolvedProvider,
					Output: &provider,
					Schema: &providerSchema,
				},
				&EvalReadStateDeposed{
					Addr:           addr.Resource,
					ProviderSchema: &providerSchema,
					Key:            n.DeposedKey,
					Output:         &state,
				},
				&EvalRefresh{
					Addr:           addr.Resource,
					ProviderAddr:   n.ResolvedProvider,
					Provider:       &provider,
					ProviderSchema: &providerSchema,
					State:          &state,
					Output:         &state,
				},
				&EvalWriteStateDeposed{
					Addr:           addr.Resource,
					Key:            n.DeposedKey,
					ProviderAddr:   n.ResolvedProvider,
					ProviderSchema: &providerSchema,
					State:          &state,
				},
			},
		},
	})

	// Apply
	var change *plans.ResourceInstanceChange
	var err error
	seq.Nodes = append(seq.Nodes, &EvalOpFilter{
		Ops: []walkOperation{walkApply, walkDestroy},
		Node: &EvalSequence{
			Nodes: []EvalNode{
				&EvalGetProvider{
					Addr:   n.ResolvedProvider,
					Output: &provider,
					Schema: &providerSchema,
				},
				&EvalReadStateDeposed{
					Addr:           addr.Resource,
					Output:         &state,
					Key:            n.DeposedKey,
					Provider:       &provider,
					ProviderSchema: &providerSchema,
				},
				&EvalDiffDestroy{
					Addr:   addr.Resource,
					State:  &state,
					Output: &change,
				},
				// Call pre-apply hook
				&EvalApplyPre{
					Addr:   addr.Resource,
					State:  &state,
					Change: &change,
				},
				&EvalApply{
					Addr:           addr.Resource,
					Config:         nil, // No configuration because we are destroying
					State:          &state,
					Change:         &change,
					Provider:       &provider,
					ProviderAddr:   n.ResolvedProvider,
					ProviderSchema: &providerSchema,
					Output:         &state,
					Error:          &err,
				},
				// Always write the resource back to the state deposed... if it
				// was successfully destroyed it will be pruned. If it was not, it will
				// be caught on the next run.
				&EvalWriteStateDeposed{
					Addr:           addr.Resource,
					Key:            n.DeposedKey,
					ProviderAddr:   n.ResolvedProvider,
					ProviderSchema: &providerSchema,
					State:          &state,
				},
				&EvalApplyPost{
					Addr:  addr.Resource,
					State: &state,
					Error: &err,
				},
				&EvalReturnError{
					Error: &err,
				},
				&EvalUpdateStateHook{},
			},
		},
	})

	return seq
}

// GraphNodeDeposer is an optional interface implemented by graph nodes that
// might create a single new deposed object for a specific associated resource
// instance, allowing a caller to optionally pre-allocate a DeposedKey for
// it.
type GraphNodeDeposer interface {
	// SetPreallocatedDeposedKey will be called during graph construction
	// if a particular node must use a pre-allocated deposed key if/when it
	// "deposes" the current object of its associated resource instance.
	SetPreallocatedDeposedKey(key states.DeposedKey)
}

// graphNodeDeposer is an embeddable implementation of GraphNodeDeposer.
// Embed it in a node type to get automatic support for it, and then access
// the field PreallocatedDeposedKey to access any pre-allocated key.
type graphNodeDeposer struct {
	PreallocatedDeposedKey states.DeposedKey
}

func (n *graphNodeDeposer) SetPreallocatedDeposedKey(key states.DeposedKey) {
	n.PreallocatedDeposedKey = key
}
