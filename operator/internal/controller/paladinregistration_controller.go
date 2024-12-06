/*
Copyright 2024.

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

package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/hyperledger/firefly-signer/pkg/abi"
	corev1alpha1 "github.com/kaleido-io/paladin/operator/api/v1alpha1"
	"github.com/kaleido-io/paladin/toolkit/pkg/pldapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/query"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
)

var registryABI = abi.ABI{
	// function registerIdentity(bytes32 parentIdentityHash, string memory name, address owner) public
	{
		Type: abi.Function,
		Name: "registerIdentity",
		Inputs: abi.ParameterArray{
			{Name: "parentIdentityHash", Type: "bytes32"},
			{Name: "name", Type: "string"},
			{Name: "owner", Type: "address"},
		},
	},
	// function setIdentityProperty(bytes32 identityHash, string memory name, string memory value) public
	{
		Type: abi.Function,
		Name: "setIdentityProperty",
		Inputs: abi.ParameterArray{
			{Name: "identityHash", Type: "bytes32"},
			{Name: "name", Type: "string"},
			{Name: "value", Type: "string"},
		},
	},
}

// PaladinRegistrationReconciler reconciles a PaladinRegistration object
type PaladinRegistrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// allows generic functions by giving a mapping between the types and interfaces for the CR
var PaladinRegistrationCRMap = CRMap[corev1alpha1.PaladinRegistration, *corev1alpha1.PaladinRegistration, *corev1alpha1.PaladinRegistrationList]{
	NewList: func() *corev1alpha1.PaladinRegistrationList { return new(corev1alpha1.PaladinRegistrationList) },
	ItemsFor: func(list *corev1alpha1.PaladinRegistrationList) []corev1alpha1.PaladinRegistration {
		return list.Items
	},
	AsObject: func(item *corev1alpha1.PaladinRegistration) *corev1alpha1.PaladinRegistration { return item },
}

func (r *PaladinRegistrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// TODO: Add an admission webhook to make the bytecode and ABI immutable

	// Fetch the PaladinRegistration instance
	var reg corev1alpha1.PaladinRegistration
	if err := r.Get(ctx, req.NamespacedName, &reg); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get PaladinRegistration resource")
		return ctrl.Result{}, err
	}
	// We wait till the registry CR is ready first
	registryAddr, err := r.getRegistryAddress(ctx, &reg)
	if err != nil {
		return ctrl.Result{}, err
	} else if registryAddr == nil {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil // we're waiting
	}
	publishCount := 0

	// First reconcile until we've submitting the registration tx
	regTx := newTransactionReconcile(r.Client,
		"reg."+reg.Name,
		reg.Spec.RegistryAdminNode /* for the root entry */, reg.Namespace,
		&reg.Status.RegistrationTx,
		func() (bool, *pldapi.TransactionInput, error) { return r.buildRegistrationTX(ctx, &reg, registryAddr) },
	)
	err = regTx.reconcile(ctx)
	if err != nil {
		// There's nothing to notify us when the world changes other than polling, so we keep re-trying
		return ctrl.Result{}, err
	} else if regTx.statusChanged {
		if reg.Status.PublishTxs == nil {
			reg.Status.PublishTxs = map[string]corev1alpha1.TransactionSubmission{}
		}
		return r.updateStatusAndRequeue(ctx, &reg, publishCount)
	} else if regTx.failed {
		return ctrl.Result{}, nil // don't go any further
	} else if !regTx.succeeded {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil // we're waiting
	}
	publishCount++

	changed := false
	requeueAfter := 0 * time.Second

	// Now we need to run a TX for each transport (we'll check availability for each before we submit)
	for _, transportName := range reg.Spec.Transports {
		transportPublishStatus := reg.Status.PublishTxs[transportName]
		regTx := newTransactionReconcile(r.Client,
			"reg."+reg.Name+"."+transportName,
			reg.Spec.Node /* the node owns their transports */, reg.Namespace,
			&transportPublishStatus,
			func() (bool, *pldapi.TransactionInput, error) {
				return r.buildTransportTX(ctx, &reg, registryAddr, transportName)
			},
		)
		err := regTx.reconcile(ctx)
		if err != nil {
			// There is nothing we can do, try the next transport
			log.Error(err, "Failed to reconcile transport transaction", "transport", transportName)
			continue
		} else if regTx.statusChanged {
			reg.Status.PublishTxs[transportName] = transportPublishStatus
			if transportPublishStatus.TransactionStatus == corev1alpha1.TransactionStatusSuccess {
				publishCount++
			}
			changed = true
		} else if regTx.failed {
			// what if one transaction failed and the other succeeded?
			// continue to try the other transactions
			log.Error(fmt.Errorf("transaction failed"), "Failed to reconcile transport transaction", "transport", transportName)
			continue
		} else if !regTx.succeeded {
			// wait before requeueing
			requeueAfter = time.Second
		}
	}

	if changed {
		// at least one transport has changed
		return r.updateStatusAndRequeue(ctx, &reg, publishCount)
	}

	// Nothing left to do
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}
func (r *PaladinRegistrationReconciler) reconcileRegistry(ctx context.Context, obj client.Object) []ctrl.Request {
	registry, ok := obj.(*corev1alpha1.PaladinRegistry)
	if !ok {
		log.FromContext(ctx).Error(fmt.Errorf("unexpected object type"), "expected Paladin")
		return nil
	}

	if registry.Status.Status != corev1alpha1.RegistryStatusAvailable {
		return nil
	}

	regs := &corev1alpha1.PaladinRegistrationList{}
	r.Client.List(ctx, regs, client.InNamespace(registry.Namespace))
	reqs := make([]ctrl.Request, 0, len(regs.Items))

	for _, reg := range regs.Items {
		if reg.Spec.Node == registry.Name {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&reg)})
		}
	}
	return reqs
}

func (r *PaladinRegistrationReconciler) updateStatusAndRequeue(ctx context.Context, reg *corev1alpha1.PaladinRegistration, publishCount int) (ctrl.Result, error) {
	reg.Status.PublishCount = publishCount
	if err := r.Status().Update(ctx, reg); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update Paladin registration status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil // Run again immediately to submit
}

func (r *PaladinRegistrationReconciler) getRegistryAddress(ctx context.Context, reg *corev1alpha1.PaladinRegistration) (*tktypes.EthAddress, error) {

	// Get the registry CR for the address
	var registry corev1alpha1.PaladinRegistry
	err := r.Get(ctx, types.NamespacedName{Name: reg.Spec.Registry, Namespace: reg.Namespace}, &registry)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if registry.Status.ContractAddress == "" {
		log.FromContext(ctx).Info("waiting for registry address")
		return nil, nil
	}

	return tktypes.ParseEthAddress(registry.Status.ContractAddress)

}

func (r *PaladinRegistrationReconciler) buildRegistrationTX(ctx context.Context, reg *corev1alpha1.PaladinRegistration, registryAddr *tktypes.EthAddress) (bool, *pldapi.TransactionInput, error) {

	// We ask the node its name, so we know what to register it as
	targetNodeRPC, err := getPaladinRPC(ctx, r.Client, reg.Spec.Node, reg.Namespace)
	if err != nil || targetNodeRPC == nil {
		return false, nil, err // not ready, or error
	}
	var nodeName string
	if err := targetNodeRPC.CallRPC(ctx, &nodeName, "transport_nodeName"); err != nil || nodeName == "" {
		return false, nil, err
	}

	// We also ask it to resolve its key down to an address
	addr, err := targetNodeRPC.KeyManager().ResolveEthAddress(ctx, reg.Spec.NodeKey)
	if err != nil {
		return false, nil, err
	}

	registration := map[string]any{
		"parentIdentityHash": tktypes.Bytes32{}, // zero for root
		"name":               nodeName,
		"owner":              addr,
	}

	tx := &pldapi.TransactionInput{
		TransactionBase: pldapi.TransactionBase{
			Type:     pldapi.TransactionTypePublic.Enum(),
			To:       registryAddr,
			Function: registryABI.Functions()["registerIdentity"].String(),
			From:     reg.Spec.RegistryAdminKey, // registry admin registers the root entry for the node
			Data:     tktypes.JSONString(registration),
		},
		ABI: registryABI,
	}

	return true, tx, nil
}

func (r *PaladinRegistrationReconciler) buildTransportTX(ctx context.Context, reg *corev1alpha1.PaladinRegistration, registryAddr *tktypes.EthAddress, transportName string) (bool, *pldapi.TransactionInput, error) {

	// Get the details from the node
	regNodeRPC, err := getPaladinRPC(ctx, r.Client, reg.Spec.Node, reg.Namespace)
	if err != nil || regNodeRPC == nil {
		return false, nil, err // not ready, or error
	}
	var transportDetails string
	if err := regNodeRPC.CallRPC(ctx, &transportDetails, "transport_localTransportDetails", transportName); err != nil || transportDetails == "" {
		return false, nil, err
	}
	var nodeName string
	if err := regNodeRPC.CallRPC(ctx, &nodeName, "transport_nodeName"); err != nil || nodeName == "" {
		return false, nil, err
	}

	// We also wait until this node has indexed the registration of the root node,
	// and use that to extract the hash
	type registryEntry struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		ParentID string `json:"parentId"`
	}
	var entries []*registryEntry
	if err := regNodeRPC.CallRPC(ctx, &entries, "reg_queryEntries", reg.Spec.Registry,
		query.NewQueryBuilder().Equal(".name", nodeName).Null(".parentId").Limit(1).Query(),
		"active",
	); err != nil {
		return false, nil, err
	}
	if len(entries) == 0 {
		log.FromContext(ctx).Info("waiting for registration to be indexed by node")
		return false, nil, nil
	}

	property := map[string]any{
		"identityHash": entries[0].ID,
		"name":         fmt.Sprintf("transport.%s", transportName),
		"value":        transportDetails,
	}

	tx := &pldapi.TransactionInput{
		TransactionBase: pldapi.TransactionBase{
			Type:     pldapi.TransactionTypePublic.Enum(),
			To:       registryAddr,
			Function: registryABI.Functions()["setIdentityProperty"].String(),
			From:     reg.Spec.NodeKey, // node registers the transports
			Data:     tktypes.JSONString(property),
		},
		ABI: registryABI,
	}

	return true, tx, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PaladinRegistrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.PaladinRegistration{}).
		// Reconcile when any node status changes
		Watches(&corev1alpha1.PaladinRegistry{}, handler.EnqueueRequestsFromMapFunc(r.reconcileRegistry), reconcileEveryChange()).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Complete(r)
}
