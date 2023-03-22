package controllers

import (
	"context"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	servicehelper "k8s.io/cloud-provider/service/helpers"
	utilsnet "k8s.io/utils/net"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	annotationForceDualStack = "ezsvclb.io/force-dual-stack"
	annotationHostname       = "ezsvclb.io/hostname"
	annotationHostnameSuffix = "ezsvclb.io/hostname-suffix"
	annotationIp             = "ezsvclb.io/ip"
)

// These functions are mostly imported from the k3s project codebase (k3s/pkg/cloudprovider/servicelb.go)
// and adapted for our usecase.

// patchStatus patches the service status. If the status has not changed, this function is a no-op.
func (r ServiceReconciler) patchStatus(ctx context.Context, svc corev1.Service, newStatus *corev1.LoadBalancerStatus) error {
	log := log.FromContext(ctx).WithValues("service", types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace})

	previousStatus := svc.Status.LoadBalancer.DeepCopy()
	if servicehelper.LoadBalancerStatusEqual(previousStatus, newStatus) {
		return nil
	}

	log.WithValues("previousStatus", previousStatus).WithValues("newStatus", newStatus).Info("Patching the service load balancer")

	updatedSvc := svc.DeepCopy()
	updatedSvc.Status.LoadBalancer = *newStatus
	err := r.Status().Update(ctx, updatedSvc)
	if err != nil {
		return err
	}

	if len(newStatus.Ingress) == 0 {
		r.Recorder.Event(&svc, corev1.EventTypeWarning, "UnAvailableLoadBalancer", "There are no available nodes for LoadBalancer")
	} else {
		r.Recorder.Eventf(&svc, corev1.EventTypeNormal, "UpdatedLoadBalancer", "Updated LoadBalancer with new IPs: %v -> %v", previousStatus, newStatus)
	}
	return nil
}

// getStatus returns a LoadBalancerStatus listing ingress IPs for all ready pods
// matching the selected service.
func (r ServiceReconciler) getStatus(ctx context.Context, req ctrl.Request, svc corev1.Service) (*corev1.LoadBalancerStatus, error) {
	nodes, err := r.getRelevantNodes(ctx, req, svc)
	if err != nil {
		return nil, err
	}

	status := corev1.LoadBalancerStatus{}
	if hasHostnameSupport(svc) {
		status.Ingress = getHostnameStatus(nodes)
	}

	if !hasIPSupport(svc) {
		return &status, nil
	}

	status.Ingress = append(status.Ingress, getIPStatus(nodes, svc)...)
	return &status, nil
}

func (r ServiceReconciler) getRelevantNodes(ctx context.Context, req ctrl.Request, svc corev1.Service) ([]corev1.Node, error) {
	relevantsNodes := []corev1.Node{}
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

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return relevantsNodes, err
	}

	for _, node := range nodes.Items {
		if !readyNodes[node.Name] {
			continue
		}

		relevantsNodes = append(relevantsNodes, node)
	}
	return relevantsNodes, nil
}

func hasDualStackSupport(svc corev1.Service) bool {
	return strings.ToLower(svc.Annotations[annotationForceDualStack]) == "true"
}

func hasHostnameSupport(svc corev1.Service) bool {
	return strings.ToLower(svc.Annotations[annotationHostname]) == "true"
}

func hasIPSupport(svc corev1.Service) bool {
	annotationIpValue := strings.ToLower(svc.Annotations[annotationIp])
	return annotationIpValue == "true" || annotationIpValue == ""
}

// Entries are sorted by hostname.
func getHostnameStatus(nodes []corev1.Node) []corev1.LoadBalancerIngress {
	hostnames := make([]string, len(nodes))
	for i, node := range nodes {
		hostnames[i] = node.Name
	}
	sort.Strings(hostnames)

	statuses := make([]corev1.LoadBalancerIngress, len(nodes))
	for i, hostname := range hostnames {
		statuses[i] = corev1.LoadBalancerIngress{Hostname: hostname}
	}

	return statuses
}

// If at least one node has External IPs available, only external IPs are returned.
// If no nodes have External IPs set, the Internal IPs of all nodes running pods are returned.
// Entries are sorted by IP address.
func getIPStatus(nodes []corev1.Node, svc corev1.Service) []corev1.LoadBalancerIngress {
	// to determine the unique set of IPs in use by pods.
	extIPs := map[string]bool{}
	intIPs := map[string]bool{}

	for _, node := range nodes {
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

	ips = filterByIPFamily(ips, svc)
	sort.Strings(ips)

	statuses := make([]corev1.LoadBalancerIngress, len(ips))
	for i, ip := range ips {
		statuses[i] = corev1.LoadBalancerIngress{IP: ip}
	}

	return statuses
}

// filterByIPFamily filters node IPs based on dual-stack parameters of the service
func filterByIPFamily(ips []string, svc corev1.Service) []string {
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

	if hasDualStackSupport(svc) {
		allAddresses = append(allAddresses, ipv4Addresses...)
		allAddresses = append(allAddresses, ipv6Addresses...)
		return allAddresses
	}

	for _, ipFamily := range svc.Spec.IPFamilies {
		switch ipFamily {
		case corev1.IPv4Protocol:
			allAddresses = append(allAddresses, ipv4Addresses...)
		case corev1.IPv6Protocol:
			allAddresses = append(allAddresses, ipv6Addresses...)
		}
	}
	return allAddresses
}
