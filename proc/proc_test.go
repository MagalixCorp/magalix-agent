package proc

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MagalixTechnologies/agent/watcher"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/health-go"
	"github.com/stretchr/testify/assert"
	kapi "k8s.io/api/core/v1"
)

type result struct {
	*watcher.ContainerStatusSource
	entity string
	id     uuid.UUID
	status watcher.Status
}

type testcase struct {
	desc     string
	services map[uuid.UUID][]uuid.UUID
	events   []interface{}
	results  []result
}

type APIGatewayMock struct {
	services map[uuid.UUID][]uuid.UUID
	results  []result
}

func (mock *APIGatewayMock) setAppServices(app string, service ...string) {
	ids := []uuid.UUID{}
	for _, service := range service {
		ids = append(ids, id(service))
	}

	mock.services[id(app)] = ids
}

func (mock *APIGatewayMock) GetApplicationDesiredServices(appID uuid.UUID) ([]uuid.UUID, error) {
	return mock.services[appID], nil
}

func (mock *APIGatewayMock) ChangeStatus(
	entity string, id uuid.UUID, status watcher.Status, source *watcher.ContainerStatusSource,
) {
	mock.results = append(mock.results, result{
		entity:                entity,
		id:                    id,
		status:                status,
		ContainerStatusSource: source,
	})
	return
}

func init() {
	logger = log.New(true, false, "/dev/stderr")
}

func TestPods(t *testing.T) {
	test := assert.New(t)

	testcases := []testcase{
		{
			"pod 1 pending, no replicas",
			services(
				"app-1", "svc-1",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusPending),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
			),
		},
		{
			"pod running, no replicas",
			services(
				"app-1", "svc-1",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
			),
		},
		{
			"pod running, replicas: 1",
			services(
				"app-1", "svc-1",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				replica("acc-1", "app-1", "svc-1", 1),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"app-1", watcher.StatusRunning,
			),
		},
		{
			"replicas: 1, pod running",
			services(
				"app-1", "svc-1",
			),
			events(
				replica("acc-1", "app-1", "svc-1", 1),
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"app-1", watcher.StatusRunning,
			),
		},
		{
			"no pods, replicas: 1",
			services(
				"app-1", "svc-1",
			),
			events(
				replica("acc-1", "app-1", "svc-1", 1),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
			),
		},
		// TODO: is it should be StatusError?
		{
			"replicas: 1, pod pending, then terminating, then terminated",
			services(
				"app-1", "svc-1",
			),
			events(
				replica("acc-1", "app-1", "svc-1", 1),
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusPending),
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusTerminating),
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusTerminated),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusError,
				"app-1", watcher.StatusError,
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
			),
		},
		{
			"replicas: 1, two pods running",
			services(
				"app-1", "svc-1",
			),
			events(
				replica("acc-1", "app-1", "svc-1", 1),
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				pod("pod-2", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"app-1", watcher.StatusRunning,
			),
		},
		{
			"replicas: 1, two pods running, replicas: 0",
			services(
				"app-1", "svc-1",
			),
			events(
				replica("acc-1", "app-1", "svc-1", 1),
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				pod("pod-2", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				replica("acc-1", "app-1", "svc-1", 0),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"app-1", watcher.StatusRunning,
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
			),
		},
		{
			"1 service pending, 1 service running, no duplicates",
			services(
				"app-1", "svc-1", "svc-2",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusPending),
				replica("acc-1", "app-1", "svc-1", 1),
				replica("acc-1", "app-1", "svc-2", 1),
				pod("pod-2", "acc-1", "app-1", "svc-2", watcher.StatusRunning),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-2", watcher.StatusPending,
				"svc-2", watcher.StatusRunning,
			),
		},
		{
			"1 service running, 1 service running",
			services(
				"app-1", "svc-1", "svc-2",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				replica("acc-1", "app-1", "svc-1", 1),
				replica("acc-1", "app-1", "svc-2", 1),
				pod("pod-2", "acc-1", "app-1", "svc-2", watcher.StatusRunning),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"svc-2", watcher.StatusPending,
				"svc-2", watcher.StatusRunning,
				"app-1", watcher.StatusRunning,
			),
		},
		{
			"1 service running, 1 service terminating",
			services(
				"app-1", "svc-1", "svc-2",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				replica("acc-1", "app-1", "svc-1", 1),
				replica("acc-1", "app-1", "svc-2", 1),
				pod("pod-2", "acc-1", "app-1", "svc-2", watcher.StatusTerminating),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"svc-2", watcher.StatusPending,
				"svc-2", watcher.StatusError,
				"app-1", watcher.StatusError,
			),
		},
		{
			"1 service running, 1 service completed",
			services(
				"app-1", "svc-1", "svc-2",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				replica("acc-1", "app-1", "svc-1", 1),
				replica("acc-1", "app-1", "svc-2", 1),
				pod("pod-2", "acc-1", "app-1", "svc-2", watcher.StatusCompleted),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"svc-2", watcher.StatusPending,
				"svc-2", watcher.StatusCompleted,
				"app-1", watcher.StatusRunning,
			),
		},
		{
			"1 service running, 1 service completed and then both completed",
			services(
				"app-1", "svc-1", "svc-2",
			),
			events(
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusRunning),
				replica("acc-1", "app-1", "svc-1", 1),
				replica("acc-1", "app-1", "svc-2", 1),
				pod("pod-2", "acc-1", "app-1", "svc-2", watcher.StatusCompleted),
				pod("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusCompleted),
			),
			results(
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusRunning,
				"svc-2", watcher.StatusPending,
				"svc-2", watcher.StatusCompleted,
				"app-1", watcher.StatusRunning,
				"svc-1", watcher.StatusCompleted,
				"app-1", watcher.StatusCompleted,
			),
		},
		{
			"pod running, replicas: 1",
			services(
				"app-1", "svc-1",
			),
			events(
				containers("pod-1", "acc-1", "app-1", "svc-1", watcher.StatusCompleted,
					map[uuid.UUID]watcher.ContainerState{
						uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
							Terminated: &kapi.ContainerStateTerminated{
								Reason: "Completed",
							},
						},
							kapi.ContainerState{
								Running: &kapi.ContainerStateRunning{},
							},
						},
					},
				),
				replica("acc-1", "app-1", "svc-1", 1),
			),
			results(
				"cnt-1", watcher.StatusCompleted, watcher.StatusReasonCompleted, 0,
				"svc-1", watcher.StatusPending,
				"app-1", watcher.StatusPending,
				"svc-1", watcher.StatusCompleted,
				"app-1", watcher.StatusCompleted,
			),
		},
	}

	for _, run := range testcases {
		if !runTestcase(test, run) {
			test.FailNow("testcase failed")
			break
		}
	}
}

func TestProc_GetPodStatus_ReturnsTerminatedAsIs(t *testing.T) {
	test := assert.New(t)

	status := GetPodStatus(Pod{
		Status: watcher.StatusTerminated,
		Containers: map[uuid.UUID]watcher.ContainerState{
			uuid.Nil: watcher.ContainerState{},
		},
	})

	test.EqualValues(status.String(), watcher.StatusTerminated.String())
}

func TestProc_GetPodStatus_CalculatesStatusUsingContainersState(t *testing.T) {
	test := assert.New(t)

	testcases := []struct {
		containers map[uuid.UUID]watcher.ContainerState
		status     watcher.Status
	}{
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusRunning,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusRunning,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Waiting: &kapi.ContainerStateWaiting{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusPending,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusError,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusRunning,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusRunning,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusCompleted,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusError,
		},
		{
			map[uuid.UUID]watcher.ContainerState{
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Terminated: &kapi.ContainerStateTerminated{
						Reason: "Completed",
					},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
				uuid.NewV4(): watcher.ContainerState{kapi.ContainerState{
					Waiting: &kapi.ContainerStateWaiting{},
				}, kapi.ContainerState{
					Running: &kapi.ContainerStateRunning{},
				}},
			},
			watcher.StatusPending,
		},
		{
			map[uuid.UUID]watcher.ContainerState{},
			watcher.StatusUnknown,
		},
	}

	for _, testcase := range testcases {
		status := GetPodStatus(Pod{Containers: testcase.containers})

		message, _ := json.MarshalIndent(testcase.containers, "", "  ")

		test.EqualValues(
			testcase.status.String(),
			status.String(),
			string(message),
		)
	}
}

func TestProc_GetContainerStateStatus(t *testing.T) {
	test := assert.New(t)

	testcases := []struct {
		state  kapi.ContainerState
		status watcher.Status
		source *watcher.ContainerStatusSource
	}{
		{
			kapi.ContainerState{
				Running: &kapi.ContainerStateRunning{},
			},
			watcher.StatusRunning,
			nil,
		},
		{
			kapi.ContainerState{
				Terminated: &kapi.ContainerStateTerminated{
					Reason: "OOMKilled",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				int32ref(0), nil, watcher.StatusReasonOOMKilled,
			},
		},
		{
			kapi.ContainerState{
				Terminated: &kapi.ContainerStateTerminated{
					ExitCode: 0,
					Reason:   "unknown reason",
				},
			},
			watcher.StatusTerminated,
			&watcher.ContainerStatusSource{
				int32ref(0), nil, "",
			},
		},
		{
			kapi.ContainerState{
				Terminated: &kapi.ContainerStateTerminated{
					ExitCode: 0,
					Reason:   "Completed",
				},
			},
			watcher.StatusCompleted,
			&watcher.ContainerStatusSource{
				int32ref(0), nil, watcher.StatusReasonCompleted,
			},
		},
		{
			kapi.ContainerState{
				Terminated: &kapi.ContainerStateTerminated{
					ExitCode: 1,
					Reason:   "Error",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				int32ref(1), nil, watcher.StatusReasonError,
			},
		},
		{
			kapi.ContainerState{
				Terminated: &kapi.ContainerStateTerminated{
					ExitCode: 1,
					Signal:   20,
					Reason:   "Error",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				int32ref(1), int32ref(20), watcher.StatusReasonError,
			},
		},
		{
			kapi.ContainerState{
				Terminated: &kapi.ContainerStateTerminated{},
			},
			watcher.StatusTerminated,
			&watcher.ContainerStatusSource{
				int32ref(0), nil, "",
			},
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Reason: "CrashLoopBackOff",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				nil, nil, watcher.StatusReasonCrashLoop,
			},
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Reason: "ErrImagePull",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				nil, nil, watcher.StatusReasonErrorImagePull,
			},
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Reason: "ImagePullBackOff",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				nil, nil, watcher.StatusReasonErrorImagePull,
			},
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Reason: "InvalidImageName",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				nil, nil, watcher.StatusReasonErrorImagePull,
			},
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Message: "Start Container Failed",
				},
			},
			watcher.StatusError,
			&watcher.ContainerStatusSource{
				nil, nil, watcher.StatusReasonErrorContainerStart,
			},
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Reason: "ContainerCreating",
				},
			},
			watcher.StatusPending,
			&watcher.ContainerStatusSource{
				nil, nil, watcher.StatusReasonCreating,
			},
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Message: "blah default",
				},
			},
			watcher.StatusPending,
			nil,
		},
		{
			kapi.ContainerState{
				Waiting: &kapi.ContainerStateWaiting{
					Reason: "blah default",
				},
			},
			watcher.StatusPending,
			nil,
		},
		{
			kapi.ContainerState{},
			watcher.StatusUnknown,
			nil,
		},
	}

	for _, testcase := range testcases {
		status, source := GetContainerStateStatus(
			watcher.ContainerState{testcase.state, testcase.state},
		)

		message, _ := json.MarshalIndent(testcase.state, "", "  ")

		test.EqualValues(
			testcase.status.String(),
			status.String(),
			string(message),
		)

		test.EqualValues(testcase.source, source, string(message))
	}
}

func runTestcase(test *assert.Assertions, run testcase) bool {
	logger.Infof(nil, "running testcase: %s", run.desc)

	pods, replicas, apigw, proc := suite()

	apigw.services = run.services

	proc.runThreads()
	proc.SetSynced()

	for _, event := range run.events {
		switch typed := event.(type) {
		case Pod:
			pods <- typed
		case ReplicaSpec:
			replicas <- typed
		default:
			panic("unexpected type")
		}

		proc.process(make(chan uuid.UUID))
	}

	close(proc.threadpool)
	proc.workers.Wait()

	return test.EqualValues(run.results, apigw.results, run.desc)
}

func events(event ...interface{}) []interface{} {
	return event
}

func results(args ...interface{}) []result {
	values := []result{}
	value := result{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if argString, ok := arg.(string); ok {
			var entity string
			if strings.HasPrefix(argString, "app-") {
				entity = "application"
			}

			if strings.HasPrefix(argString, "svc-") {
				entity = "service"
			}

			if strings.HasPrefix(argString, "cnt-") {
				entity = "container"
			}

			if entity == "" {
				panic("unexpected string in values: " + argString)
			}

			if value.id != uuid.Nil {
				values = append(values, value)

				value = result{}
			}

			// TODO: fix
			value.id = uuid.NewV4()
			value.entity = entity

			continue
		}

		if argStatus, ok := arg.(watcher.Status); ok {
			value.status = argStatus
			continue
		}

		if argReason, ok := arg.(watcher.StatusReason); ok {
			if value.ContainerStatusSource == nil {
				value.ContainerStatusSource = &watcher.ContainerStatusSource{}
				value.Reason = argReason
				continue
			}

			panic("unexepected status reason: " + argReason)
		}

		if arg == nil {
			if value.ContainerStatusSource == nil {
				value.ContainerStatusSource = &watcher.ContainerStatusSource{}
			}

			continue
		}

		if argInt, ok := arg.(int); ok {
			argInt32 := int32(argInt)
			value.ExitCode = &argInt32

			continue
		}
	}

	values = append(values, value)

	return values
}

func services(values ...string) map[uuid.UUID][]uuid.UUID {
	key := ""
	table := map[uuid.UUID][]uuid.UUID{}
	for i := 0; i < len(values); i++ {
		if strings.HasPrefix(values[i], "app-") {
			key = values[i]
			table[id(key)] = []uuid.UUID{}
			continue
		}

		table[id(key)] = append(table[id(key)], id(values[i]))
	}

	return table
}

func pod(
	name string,
	accountID string,
	applicationID string,
	serviceID string,
	status watcher.Status,
) Pod {
	return containers(name, accountID, applicationID, serviceID, status, nil)
}

func containers(
	name string,
	accountID string,
	applicationID string,
	serviceID string,
	status watcher.Status,
	containers map[uuid.UUID]watcher.ContainerState,
) Pod {
	identified := map[uuid.UUID]watcher.ContainerState{}
	for _, state := range containers {
		// TODO: fix
		identified[uuid.NewV4()] = state
	}
	return Pod{
		Name:          name,
		AccountID:     id(accountID),
		ApplicationID: id(applicationID),
		ServiceID:     id(serviceID),
		Status:        status,
		Containers:    identified,
	}
}

func replica(
	accountID string,
	applicationID string,
	serviceID string,
	replicas int,
) ReplicaSpec {
	return ReplicaSpec{
		AccountID:     id(accountID),
		ApplicationID: id(applicationID),
		ServiceID:     id(serviceID),
		Replicas:      replicas,
	}
}

func id(target string) uuid.UUID {
	hasher := md5.New()
	hasher.Write([]byte(target))
	hash := hasher.Sum(nil)

	id, err := uuid.FromBytes(hash)
	if err != nil {
		panic(fmt.Sprintf("uuid.FromString: %s %s; %s", target, hex.EncodeToString(hash), err))
	}

	return id
}

func suite() (chan Pod, chan ReplicaSpec, *APIGatewayMock, *Proc) {
	var (
		pods     = make(chan Pod, 10)
		replicas = make(chan ReplicaSpec, 10)
		apigw    = &APIGatewayMock{services: map[uuid.UUID][]uuid.UUID{}}
		proc     = NewProc(pods, replicas, apigw, nil, 1, health.NewHealth())
	)

	return pods, replicas, apigw, proc
}

func int32ref(value int32) *int32 {
	return &value
}
