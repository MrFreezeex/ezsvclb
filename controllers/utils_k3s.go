package controllers

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	servicehelper "k8s.io/cloud-provider/service/helpers"
	utilsnet "k8s.io/utils/net"
	ctrl "sigs.k8s.io/controller-runtime"
)

// These functions are imported from the k3s project codebase (k3s/pkg/cloudprovider/servicelb.go)
// and slightly adapted for our usecase.

// patchStatus patches the service status. If the status has not changed, this function is a no-op.
func (r ServiceReconciler) patchStatus(ctx context.Context, svc *corev1.Service, newStatus *corev1.LoadBalancerStatus) error {
	previousStatus := svc.Status.LoadBalancer.DeepCopy()
	if servicehelper.LoadBalancerStatusEqual(previousStatus, newStatus) {
		return nil
	}

	updated := svc.DeepCopy()
	updated.Status.LoadBalancer = *newStatus
	err := r.Update(ctx, updated)
	if err != nil {
		return err
	}

	if len(newStatus.Ingress) == 0 {
		r.Recorder.Event(svc, corev1.EventTypeWarning, "UnAvailableLoadBalancer", "There are no available nodes for LoadBalancer")
	} else {
		r.Recorder.Eventf(svc, corev1.EventTypeNormal, "UpdatedLoadBalancer", "Updated LoadBalancer with new IPs: %v -> %v", previousStatus.Ingress, newStatus.Ingress)
	}
	return nil
}

// getStatus returns a LoadBalancerStatus listing ingress IPs for all ready pods
// matching the selected service.
func (r ServiceReconciler) getStatus(ctx context.Context, req ctrl.Request, svc *corev1.Service) (*corev1.LoadBalancerStatus, error) {
	var readyNodes map[string]bool

	readyNodes = map[string]bool{}
	var eps corev1.Endpoints
	if err := r.Get(ctx, req.NamespacedName, &eps); err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	for _, subset := range eps.Subsets {
		for _, endpoint := range subset.Addresses {
			if endpoint.NodeName != nil {
				readyNodes[*endpoint.NodeName] = true
			}
		}
	}

	expectedIPs, err := r.nodeIPs(ctx, svc, readyNodes)
	if err != nil {
		return nil, err
	}

	sort.Strings(expectedIPs)

	loadbalancer := &corev1.LoadBalancerStatus{}
	for _, ip := range expectedIPs {
		loadbalancer.Ingress = append(loadbalancer.Ingress, corev1.LoadBalancerIngress{
			IP: ip,
		})
	}

	return loadbalancer, nil
}

// nodeIPs returns a list of IPs for Nodes hosting ServiceLB Pods.
// If at least one node has External IPs available, only external IPs are returned.
// If no nodes have External IPs set, the Internal IPs of all nodes running pods are returned.
func (r ServiceReconciler) nodeIPs(ctx context.Context, svc *corev1.Service, readyNodes map[string]bool) ([]string, error) {
	// Go doesn't have sets so we stuff things into a map of bools and then get lists of keys
	// to determine the unique set of IPs in use by pods.
	extIPs := map[string]bool{}
	intIPs := map[string]bool{}

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		//fmt.Printf("Error fetching nodes, check your permissions: %+v\n", err)
		return nil, err
	}

	for nodeName := range readyNodes {
		node := &corev1.Node{}
		err := r.Get(ctx, types.NamespacedName{Name: nodeName}, node)
		if apierrors.IsNotFound(err) {
			continue
		} else if err != nil {
			return nil, err
		}

		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeExternalIP {
				extIPs[addr.Address] = true
			} else if addr.Type == corev1.NodeInternalIP {
				intIPs[addr.Address] = true
			}
		}
	}

	keys := func(addrs map[string]bool) (ips []string) {
		for k := range addrs {
			ips = append(ips, k)
		}
		return ips
	}

	var ips []string
	if len(extIPs) > 0 {
		ips = keys(extIPs)
	} else {
		ips = keys(intIPs)
	}

	ips, err := filterByIPFamily(ips, svc)
	if err != nil {
		return nil, err
	}

	return ips, nil
}

// filterByIPFamily filters node IPs based on dual-stack parameters of the service
func filterByIPFamily(ips []string, svc *corev1.Service) ([]string, error) {
	var ipv4Addresses []string
	var ipv6Addresses []string
	var allAddresses []string

	for _, ip := range ips {
		if utilsnet.IsIPv4String(ip) {
			ipv4Addresses = append(ipv4Addresses, ip)
		}
		if utilsnet.IsIPv6String(ip) {
			ipv6Addresses = append(ipv6Addresses, ip)
		}
	}

	for _, ipFamily := range svc.Spec.IPFamilies {
		switch ipFamily {
		case corev1.IPv4Protocol:
			allAddresses = append(allAddresses, ipv4Addresses...)
		case corev1.IPv6Protocol:
			allAddresses = append(allAddresses, ipv6Addresses...)
		}
	}
	return allAddresses, nil
}
