package kuber

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
)

type GroupVersionResourceKind struct {
	schema.GroupVersionResource
	Kind string
}

func (gvrk GroupVersionResourceKind) String() string {
	return strings.Join([]string{gvrk.Group, "/", gvrk.Version, ", Resource=", gvrk.Resource, ", Kind=", gvrk.Kind}, "")
}

var (
	Nodes = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("nodes"),
		Kind:                 "Node",
	}
	Namespaces = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("namespaces"),
		Kind:                 "Namespace",
	}
	LimitRanges = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("limitranges"),
		Kind:                 "LimitRange",
	}
	Pods = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("pods"),
		Kind:                 "Pod",
	}
	ReplicationControllers = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("replicationcontrollers"),
		Kind:                 "ReplicationController",
	}
	Deployments = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("deployments"),
		Kind:                 "Deployment",
	}
	StatefulSets = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("statefulsets"),
		Kind:                 "StatefulSet",
	}
	DaemonSets = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("daemonsets"),
		Kind:                 "DaemonSet",
	}
	ReplicaSets = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("replicasets"),
		Kind:                 "ReplicaSet",
	}
	Jobs = GroupVersionResourceKind{
		GroupVersionResource: batchv1.SchemeGroupVersion.WithResource("jobs"),
		Kind:                 "Job",
	}
	CronJobs = GroupVersionResourceKind{
		GroupVersionResource: batchv1beta1.SchemeGroupVersion.WithResource("cronjobs"),
		Kind:                 "CronJob",
	}
	Ingresses = GroupVersionResourceKind{
		GroupVersionResource: networkingv1beta1.SchemeGroupVersion.WithResource("ingresses"),
		Kind:                 "Ingress",
	}
	IngressClasses = GroupVersionResourceKind{
		GroupVersionResource: networkingv1beta1.SchemeGroupVersion.WithResource("ingressclasses"),
		Kind:                 "IngressClass",
	}
	NetworkPolicies = GroupVersionResourceKind{
		GroupVersionResource: networkingv1.SchemeGroupVersion.WithResource("networkpolicies"),
		Kind:                 "NetworkPolicy",
	}
	Services = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("services"),
		Kind:                 "Service",
	}
	PersistentVolumes = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("persistentvolumes"),
		Kind:                 "PersistentVolume",
	}
	PersistentVolumeClaims = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims"),
		Kind:                 "PersistentVolumeClaim",
	}
	StorageClasses = GroupVersionResourceKind{
		GroupVersionResource: storagev1.SchemeGroupVersion.WithResource("storageclasses"),
		Kind:                 "StorageClass",
	}
	Roles = GroupVersionResourceKind{
		GroupVersionResource: rbacv1.SchemeGroupVersion.WithResource("roles"),
		Kind:                 "Role",
	}
	RoleBindings = GroupVersionResourceKind{
		GroupVersionResource: rbacv1.SchemeGroupVersion.WithResource("rolebindings"),
		Kind:                 "RoleBinding",
	}
	ClusterRoles = GroupVersionResourceKind{
		GroupVersionResource: rbacv1.SchemeGroupVersion.WithResource("clusterroles"),
		Kind:                 "ClusterRole",
	}
	ClusterRoleBindings = GroupVersionResourceKind{
		GroupVersionResource: rbacv1.SchemeGroupVersion.WithResource("clusterrolebindings"),
		Kind:                 "ClusterRoleBinding",
	}
	ServiceAccounts = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("serviceaccounts"),
		Kind:                 "ServiceAccount",
	}

	kindToGVRK = map[string]*GroupVersionResourceKind{
		Nodes.Kind:                  &Nodes,
		Namespaces.Kind:             &Namespaces,
		Pods.Kind:                   &Pods,
		ReplicationControllers.Kind: &ReplicationControllers,
		Deployments.Kind:            &Deployments,
		StatefulSets.Kind:           &StatefulSets,
		DaemonSets.Kind:             &DaemonSets,
		ReplicaSets.Kind:            &ReplicaSets,
		Jobs.Kind:                   &Jobs,
		CronJobs.Kind:               &CronJobs,
		Ingresses.Kind:              &Ingresses,
		IngressClasses.Kind:         &IngressClasses,
		NetworkPolicies.Kind:        &NetworkPolicies,
		Services.Kind:               &Services,
		PersistentVolumes.Kind:      &PersistentVolumes,
		PersistentVolumeClaims.Kind: &PersistentVolumeClaims,
		StorageClasses.Kind:         &StorageClasses,
		Roles.Kind:                  &Roles,
		RoleBindings.Kind:           &RoleBindings,
		ClusterRoles.Kind:           &ClusterRoles,
		ClusterRoleBindings.Kind:    &ClusterRoleBindings,
		ServiceAccounts.Kind:        &ServiceAccounts,
	}
)

func KindToGVRK(kind string) (*GroupVersionResourceKind, error) {
	gvrk, ok := kindToGVRK[kind]
	if !ok {
		return nil, fmt.Errorf("unknown kind %s", kind)
	}
	return gvrk, nil
}
