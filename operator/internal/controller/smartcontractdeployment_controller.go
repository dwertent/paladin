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
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Masterminds/sprig/v3"
	corev1alpha1 "github.com/kaleido-io/paladin/operator/api/v1alpha1"
	"github.com/kaleido-io/paladin/toolkit/pkg/pldapi"
	"github.com/kaleido-io/paladin/toolkit/pkg/solutils"
	"github.com/kaleido-io/paladin/toolkit/pkg/tktypes"
)

// SmartContractDeploymentReconciler reconciles a SmartContractDeployment object
type SmartContractDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// allows generic functions by giving a mapping between the types and interfaces for the CR
var SmartContractDeploymentCRMap = CRMap[corev1alpha1.SmartContractDeployment, *corev1alpha1.SmartContractDeployment, *corev1alpha1.SmartContractDeploymentList]{
	NewList: func() *corev1alpha1.SmartContractDeploymentList { return new(corev1alpha1.SmartContractDeploymentList) },
	ItemsFor: func(list *corev1alpha1.SmartContractDeploymentList) []corev1alpha1.SmartContractDeployment {
		return list.Items
	},
	AsObject: func(item *corev1alpha1.SmartContractDeployment) *corev1alpha1.SmartContractDeployment { return item },
}

func (r *SmartContractDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// TODO: Add an admission webhook to make the bytecode and ABI immutable

	// Fetch the SmartContractDeployment instance
	var scd corev1alpha1.SmartContractDeployment
	if err := r.Get(ctx, req.NamespacedName, &scd); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get SmartContractDeployment resource")
		return ctrl.Result{}, err
	}

	// Check all our deps are resolved
	depsChanged, ready, err := checkSmartContractDeps(ctx, r.Client, scd.Namespace, scd.Spec.RequiredContractDeployments, &scd.Status.ContactDependenciesStatus)
	if err != nil {
		return ctrl.Result{}, err
	} else if depsChanged {
		return r.updateStatusAndRequeue(ctx, &scd)
	} else if !ready {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// Reconcile the deployment transaction
	txReconcile := newTransactionReconcile(r.Client,
		"scdeploy."+scd.Name,
		scd.Spec.Node, scd.Namespace,
		&scd.Status.TransactionSubmission,
		func() (bool, *pldapi.TransactionInput, error) { return r.buildDeployTransaction(ctx, &scd) },
	)
	err = txReconcile.reconcile(ctx)
	if err != nil {
		// There's nothing to notify us when the world changes other than polling, so we keep re-tryingat
		// a fixed rate (matching the readiness probe period of Paladin) to avoid any exponential backoff
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	} else if txReconcile.statusChanged {
		// Common TX reconciler does everything for us apart from grab the receipt
		if scd.Status.TransactionStatus == corev1alpha1.TransactionStatusSuccess && scd.Status.ContractAddress == "" {
			if txReconcile.receipt.ContractAddress == nil {
				scd.Status.TransactionStatus = corev1alpha1.TransactionStatusFailed
				scd.Status.FailureMessage = "transaction did not result in contract deployment"
			} else {
				scd.Status.ContractAddress = txReconcile.receipt.ContractAddress.String()
			}
		}
		return r.updateStatusAndRequeue(ctx, &scd)
	} else if !txReconcile.failed && !txReconcile.succeeded {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}
	// Nothing left to do - we succeeded or failed
	return ctrl.Result{}, nil
}

func (r *SmartContractDeploymentReconciler) updateStatusAndRequeue(ctx context.Context, scd *corev1alpha1.SmartContractDeployment) (ctrl.Result, error) {
	if err := r.Status().Update(ctx, scd); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update smart contract deployment status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil // Run again immediately to submit
}

func (r *SmartContractDeploymentReconciler) buildDeployTransaction(ctx context.Context, scd *corev1alpha1.SmartContractDeployment) (bool, *pldapi.TransactionInput, error) {
	var data tktypes.RawJSON
	if scd.Spec.ParamsJSON == "" {
		data = tktypes.RawJSON(scd.Spec.ParamsJSON)
	}
	build := solutils.SolidityBuildWithLinks{
		Bytecode: scd.Spec.Bytecode,
	}
	if err := json.Unmarshal([]byte(scd.Spec.ABIJSON), &build.ABI); err != nil {
		return false, nil, fmt.Errorf("invalid ABI: %s", err)
	}
	if scd.Spec.LinkReferencesJSON != "" {
		if err := json.Unmarshal([]byte(scd.Spec.LinkReferencesJSON), &build.LinkReferences); err != nil {
			return false, nil, fmt.Errorf("invalid linkReferences: %s", err)
		}
	}
	linkReferences, err := r.buildLinkReferences(scd)
	if err != nil {
		return false, nil, err
	}
	bytecode, err := build.ResolveLinks(ctx, linkReferences)
	if err != nil {
		return false, nil, err
	}

	return true, &pldapi.TransactionInput{
		TransactionBase: pldapi.TransactionBase{
			Type:   tktypes.Enum[pldapi.TransactionType](scd.Spec.TxType),
			Domain: scd.Spec.Domain,
			From:   scd.Spec.From,
			Data:   data,
		},
		ABI:      build.ABI,
		Bytecode: bytecode,
	}, nil
}

func (r *SmartContractDeploymentReconciler) buildLinkReferences(scd *corev1alpha1.SmartContractDeployment) (map[string]*tktypes.EthAddress, error) {

	var crMap map[string]any
	linkedAddresses := map[string]*tktypes.EthAddress{}

	for libName, addrTemplateStr := range scd.Spec.LinkedContracts {

		t, err := template.New("").Option("missingkey=error").Funcs(sprig.FuncMap()).Parse(addrTemplateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid Go template for linked contract %s: %s", libName, err)
		}

		if crMap == nil {
			crJSON, err := json.Marshal(scd)
			if err == nil {
				err = json.Unmarshal(crJSON, &crMap)
			}
			if err != nil {
				return nil, err
			}
		}

		addrBuff := new(strings.Builder)
		if err = t.Execute(addrBuff, crMap); err != nil {
			return nil, fmt.Errorf("go template failed for linked contract %s: %s", libName, err)
		}

		addr, err := tktypes.ParseEthAddress(addrBuff.String())
		if err != nil {
			return nil, fmt.Errorf("invalid address '%s' for resolved library %s: %s", addrBuff, libName, err)
		}
		linkedAddresses[libName] = addr

	}
	return linkedAddresses, nil
}

func (r *SmartContractDeploymentReconciler) reconcilePaladin(ctx context.Context, obj client.Object) []ctrl.Request {
	paladin, ok := obj.(*corev1alpha1.Paladin)
	if !ok {
		log.FromContext(ctx).Error(fmt.Errorf("unexpected object type"), "expected Paladin")
		return nil
	}

	if paladin.Status.Phase != corev1alpha1.StatusPhaseReady {
		return nil
	}

	scds := &corev1alpha1.SmartContractDeploymentList{}
	r.Client.List(ctx, scds, client.InNamespace(paladin.Namespace))
	reqs := make([]ctrl.Request, 0, len(scds.Items))

	for _, scd := range scds.Items {
		if scd.Spec.Node == paladin.Name {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&scd)})
		}
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager.
func (r *SmartContractDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.SmartContractDeployment{}).
		// Reconcile when any node status changes
		Watches(&corev1alpha1.Paladin{}, handler.EnqueueRequestsFromMapFunc(r.reconcilePaladin), reconcileEveryChange()).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 5,
		}).
		Complete(r)
}