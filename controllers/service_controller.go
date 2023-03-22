/*
Copyright 2023 Arthur Outhenin-Chalandre.

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

package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const loadBalancerClass = "ezsvclb"

// ServiceReconciler reconciles a Service object
type ServiceReconciler struct {
	client.Client
	Scheme                    *runtime.Scheme
	Recorder                  record.EventRecorder
	EnableDefaultLoadBalancer bool
}

// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=services/status,verbs=update;patch
// +kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("service", req.NamespacedName)

	svc := &corev1.Service{}
	err := r.Get(ctx, req.NamespacedName, svc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "unable to fetch Service")
		return ctrl.Result{}, err
	}

	log.Info("Start reconciling Service")
	defer log.Info("End reconciling Service")

	if !r.isServiceSupported(svc) {
		log.Info("Service is not supported by the controller")
		return ctrl.Result{}, nil
	}

	newStatus, err := r.getStatus(ctx, req, svc)
	if err != nil {
		log.Error(err, "unable to get Service status")
		return ctrl.Result{}, err
	}
	if err = r.patchStatus(ctx, svc, newStatus); err != nil {
		log.Error(err, "Unable to patch Service status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// isServiceSupported returns true if the service is supported by the controller
func (r ServiceReconciler) isServiceSupported(service *corev1.Service) bool {
	if !service.DeletionTimestamp.IsZero() {
		return false
	}
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return false
	}
	if service.Spec.LoadBalancerClass != nil {
		return *service.Spec.LoadBalancerClass == loadBalancerClass
	}
	return r.EnableDefaultLoadBalancer
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Watches(&source.Kind{Type: &v1.Endpoints{}},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
				endpoints, ok := obj.(*v1.Endpoints)
				if !ok {
					log.Log.Error(fmt.Errorf("Can't cast to an endpoint"), "Received a non endpoint object")
					return []ctrl.Request{}
				}
				name := types.NamespacedName{Name: endpoints.Name, Namespace: endpoints.Namespace}
				return []ctrl.Request{{NamespacedName: name}}
			})).
		Watches(&source.Kind{Type: &v1.Node{}},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
				_, ok := obj.(*v1.Node)
				if !ok {
					log.Log.Error(fmt.Errorf("Can't cast to a node"), "Received a non node object")
					return []ctrl.Request{}
				}

				var services corev1.ServiceList
				if err := r.List(context.TODO(), &services); err != nil {
					log.Log.Error(err, "Can't list services")
					return []ctrl.Request{}
				}

				// Attempt to resync all supported service if a node change
				requests := make([]ctrl.Request, len(services.Items))
				for i, service := range services.Items {
					if r.isServiceSupported(&service) {
						name := types.NamespacedName{Name: service.Name, Namespace: service.Namespace}
						requests[i] = ctrl.Request{NamespacedName: name}
					}
				}
				return requests
			})).
		Complete(r)
}
