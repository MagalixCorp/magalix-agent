package executor

import (
	"strings"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixTechnologies/core/logger"

	kv1 "k8s.io/api/core/v1"
)

func (executor *Executor) podsStatusHandler(entityName string, namespace string, kind string, statusMap map[kv1.PodPhase]string) (result agent.AutomationStatus, msg string, targetPods int32, runningPods int32) {
	// short pooling to trigger pod status with max 15 minutes
	msg = "pods restarting exceeded timout (15 min)"
	start := time.Now()

	objectName := ""
	result = agent.AutomationFailed
	targetPods = 0
	var err error
	flag := false

	if strings.ToLower(kind) == "statefulset" {
		objectName, targetPods, err = executor.statefulsetsHandler(entityName, namespace)
		if err != nil {
			flag = true

		}

	} else if strings.ToLower(kind) == "daemonset" {
		objectName, targetPods, err = executor.daemonsetsHandler(entityName, namespace)
		if err != nil {
			flag = true

		}

	} else if strings.ToLower(kind) == "job" || strings.ToLower(kind) == "cronjob" {
		job, err := executor.kube.GetCronJob(namespace, entityName)

		if err != nil {
			flag = true

		} else {
			// get the new job
			objectName = job.Name
			targetPods = 1

		}
	}

	if flag {
		msg = "failed to trigger pod status"
		result = agent.AutomationFailed

	} else {
		for time.Since(start) < automationsExecutionTimeout {

			time.Sleep(podStatusSleep)

			// In case of deployment we make sure to update replicaset in each iteration to get the current replica sets with ready replicas and not the previous one
			if strings.ToLower(kind) == "deployment" {
				objectName, targetPods, err = executor.deploymentsHandler(entityName, namespace)
				if err != nil {
					msg = "failed to trigger pod status"
					result = agent.AutomationFailed
					break
				}
			}

			status := kv1.PodPending

			pods, err := executor.kube.GetNameSpacePods(namespace)

			if err != nil {
				msg = "failed to trigger pod status"
				result = agent.AutomationFailed
				break
			}

			runningPods = 0
			// TODO update the execution flow to check pods status across controllers
			for _, pod := range pods.Items {
				//handle the bug of naming convention for pods in kubernetes DEV-2046
				if strings.Contains(pod.GenerateName, objectName) {
					logger.Debugw("get pod status", "pod", pod.Name, "status", pod.Status.Phase)
					status = pod.Status.Phase
					if status == kv1.PodRunning {
						runningPods++
					} else if status != kv1.PodPending {
						break
					}
				}
			}

			if runningPods == targetPods {
				msg = statusMap[status]
				result = agent.AutomationExecuted
				break
			}
		}
	}

	return result, msg, targetPods, runningPods
}

func (executor *Executor) deploymentsHandler(entityName string, namespace string) (deploymentName string, targetPods int32, err error) {
	replicasets, err := executor.kube.GetNamespaceReplicaSets(namespace)

	if err != nil {
		return "", 0, err
	}

	// get the new replicaset
	for _, replica := range replicasets.Items {
		if strings.Contains(replica.Name, entityName) && replica.Status.Replicas > 0 {
			deploymentName = replica.Name
			targetPods = *replica.Spec.Replicas
			break
		}
	}

	return deploymentName, targetPods, nil
}

func (executor *Executor) statefulsetsHandler(entityName string, namespace string) (statefulsetName string, targetPods int32, err error) {
	statefulset, err := executor.kube.GetStatefulSet(namespace, entityName)

	if err != nil {
		return "", 0, err

	} else {
		// get the new StatefulSet
		if statefulset.Status.ReadyReplicas > 0 {
			statefulsetName = statefulset.Name
			targetPods = *statefulset.Spec.Replicas
		}
	}

	return statefulsetName, targetPods, nil
}

func (executor *Executor) daemonsetsHandler(entityName string, namespace string) (daemonsetName string, targetPods int32, err error) {

	daemonSet, err := executor.kube.GetDaemonSet(namespace, entityName)

	if err != nil {
		return "", 0, err

	} else {
		// get the new daemonSet
		if daemonSet.Status.NumberReady > 0 {
			daemonsetName = daemonSet.Name
			targetPods = daemonSet.Status.DesiredNumberScheduled
		}
	}

	return daemonsetName, targetPods, nil
}
