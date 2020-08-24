package executor

import (
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	kv1 "k8s.io/api/core/v1"
	"sort"
	"strings"
	"time"
)

func (executor *Executor) podsStatusHandler(entity_name string, namespace string, kind string, statusMap map[kv1.PodPhase]string) (result proto.DecisionExecutionStatus, msg string, targetPods int32, runningPods int32){

	// short pooling to trigger pod status with max 15 minutes
	msg = "pods restarting exceeded timout (15 min)"
	start := time.Now()

	entitiName := ""
	result = proto.DecisionExecutionStatusFailed
	targetPods = 0
	runningPods = 0
	flag := false

	if strings.ToLower(kind) == "deployment"{

		eName, pods, err := executor.deployemntsHandler(entity_name, namespace)
		entitiName = eName
		targetPods = pods

		if err != nil {
			flag = true

		}

	}else if strings.ToLower(kind) == "statefulset"{

		eName, pods, err := executor.statefulsetsHandler(entity_name, namespace)
		entitiName = eName
		targetPods = pods

		if err != nil {
			flag = true

		}

	}else if strings.ToLower(kind) == "daemonset"{

		eName, pods, err := executor.daemonsetsHandler(entity_name, namespace)
		entitiName = eName
		targetPods = pods

		if err != nil {
			flag = true

		}

	}else if strings.ToLower(kind) == "job" || strings.ToLower(kind) == "cronjob"{

		job, err := executor.kube.GetCronJob(namespace, entity_name)

		if err != nil {
			flag = true

		}else{
			// get the new job
			entitiName = job.Name
			targetPods = 1

		}
	}

	if flag {
		msg = "failed to trigger pod status"
		result = proto.DecisionExecutionStatusFailed

	}else {

		// get pods of the new controller
		for time.Now().Sub(start) < decisionsExecutionTimeout {

			status := kv1.PodPending

			time.Sleep(podStatusSleep)
			pods, err := executor.kube.GetNameSpacePods(namespace)

			if err != nil {
				msg = "failed to trigger pod status"
				result = proto.DecisionExecutionStatusFailed
				break
			}

			for _, pod := range pods.Items {
				if strings.Contains(pod.GenerateName, entitiName){
					executor.logger.Info(pod.Name, ", status: ", pod.Status.Phase)
					status = pod.Status.Phase
					if status == kv1.PodRunning {
						runningPods++
					}else if status != kv1.PodPending {
						break
					}
				}
			}

			if runningPods == targetPods {
				msg = statusMap[status]
				result = proto.DecisionExecutionStatusSucceed
				break
			}
		}
	}

	return result, msg, targetPods, runningPods
}


func (executor *Executor) deployemntsHandler (entity_name string, namespace string) (entitiName string, targetPods int32, err error){

	replicasets, err := executor.kube.GetNamespaceReplicaSets(namespace)

	if err != nil {
		return "", 0, err

	}else{

		currentReplicas := []Replica{}
		// get the new replicaset
		for _, replica := range replicasets.Items {
			if strings.Contains(replica.Name, entity_name) && replica.Status.Replicas > 0{
				currentReplicas = append(currentReplicas, Replica{replica.Name, *replica.Spec.Replicas, replica.CreationTimestamp.Local()})
			}
		}

		sort.Slice(currentReplicas, func(i, j int) bool {
			return currentReplicas[i].time.After(currentReplicas[j].time)
		})

		entitiName = currentReplicas[0].name
		targetPods = currentReplicas[0].replicas
	}

	return entitiName, targetPods, nil
}

func (executor *Executor) statefulsetsHandler (entity_name string, namespace string) (entitiName string, targetPods int32, err error){

	statefulset, err := executor.kube.GetStatefulSet(namespace, entity_name)

	if err != nil {
		return "", 0, err

	}else{
		// get the new StatefulSet
		if statefulset.Status.ReadyReplicas > 0 {
			entitiName = statefulset.Name
			targetPods = *statefulset.Spec.Replicas
		}
	}

	return entitiName, targetPods, nil
}

func (executor *Executor) daemonsetsHandler (entity_name string, namespace string) (entitiName string, targetPods int32, err error){

	daemonSet, err := executor.kube.GetDaemonSet(namespace, entity_name)

	if err != nil {
		return "", 0, err

	}else{
		// get the new daemonSet
		if daemonSet.Status.NumberReady > 0 {
			entitiName = daemonSet.Name
			targetPods = daemonSet.Status.DesiredNumberScheduled
		}
	}

	return entitiName, targetPods, nil
}