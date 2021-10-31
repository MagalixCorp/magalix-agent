package kuber

import (
	"fmt"

	"github.com/MagalixCorp/magalix-agent/v3/utils"
	corev1 "k8s.io/api/core/v1"
	kv1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	podSpecMap = map[string][]string{
		Pods.Kind:                   {"spec"},
		ReplicationControllers.Kind: {"spec", "template", "spec"},
		Deployments.Kind:            {"spec", "template", "spec"},
		StatefulSets.Kind:           {"spec", "template", "spec"},
		DaemonSets.Kind:             {"spec", "template", "spec"},
		ReplicaSets.Kind:            {"spec", "template", "spec"},
		Jobs.Kind:                   {"spec", "template", "spec"},
		CronJobs.Kind:               {"spec", "jobTemplate", "spec", "template", "spec"},
	}
)

func maskContainers(containers []kv1.Container) (masked []kv1.Container) {
	for _, container := range containers {
		container.Env = maskEnvVars(container.Env)
		container.Args = maskArgs(container.Args)
		masked = append(masked, container)
	}
	return
}

func maskEnvVars(env []kv1.EnvVar) (masked []kv1.EnvVar) {
	masked = make([]kv1.EnvVar, len(env))
	for i, envVar := range env {
		if envVar.Value != "" {
			envVar.Value = maskedValue
		}
		masked[i] = envVar
	}
	return
}

func maskArgs(args []string) (masked []string) {
	masked = make([]string, len(args))
	for i := range args {
		masked[i] = maskedValue
	}
	return
}

func maskUnstructured(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	kind := obj.GetKind()
	errMap := map[string]interface{}{
		"kind": kind,
	}

	podSpecPath, ok := podSpecMap[kind]
	if !ok {
		// not maskable kind
		return obj, nil
	}

	podSpecU, ok, err := unstructured.NestedFieldNoCopy(obj.Object, podSpecPath...)
	if err != nil {
		return nil, fmt.Errorf("unable to get pod spec, error: %w with data %+v", err, errMap)
	}
	if !ok {
		return nil, fmt.Errorf("unable to find pod spec in specified path with data %+v", errMap)
	}

	var podSpec corev1.PodSpec
	err = utils.Transcode(podSpecU, &podSpec)
	if err != nil {
		return nil, fmt.Errorf("unable to transcode pod spec, error: %w with data %+v", err, errMap)
	}

	podSpec.Containers = maskContainers(podSpec.Containers)
	podSpec.InitContainers = maskContainers(podSpec.InitContainers)

	var podSpecJson map[string]interface{}
	err = utils.Transcode(podSpec, &podSpecJson)
	if err != nil {
		return nil, fmt.Errorf("unable to transcode pod spec, error: %w with data %+v", err, errMap)
	}

	// deep copy to not mutate the data from cash store
	obj = obj.DeepCopy()
	err = unstructured.SetNestedField(obj.Object, podSpecJson, podSpecPath...)
	if err != nil {
		return nil, fmt.Errorf("unable to set pod spec, error: %w, with data %+v", err, errMap)
	}

	return obj, nil
}
