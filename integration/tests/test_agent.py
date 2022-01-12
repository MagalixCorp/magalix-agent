import requests
import os
import time
import pytest
import yaml
import uuid

URL = os.environ["URL"]
EMAIL = os.environ["EMAIL"]
PASSWORD = os.environ["PASSWORD"]
CLUSTER_NAME = "agent-integration-test-" + uuid.uuid4().hex[:5]
NAMESPACE = "agent-integration-test"
AGET_IMAGE = os.environ["AGENT_IMAGE"]


TEST_CONSTRAINTS_NAMES = [
    "Custom Policy - Using latest Image Tag",
    "Custom Policy - Services are not using ports over 1024",
    "Custom Policy - Missing Owner Label",
    "Custom Policy - Containers running with PrivilegeEscalation"
]

AGENT_YAML_PATH = "/tmp/agent_resources.yaml"


def patch_agent_resources(yml):
    resources =  yaml.safe_load_all(yml)
    output_resources = []

    for resource in resources:
        if resource.get("kind") == "Deployment":
            containers = resource["spec"]["template"]["spec"]["containers"]
            for container in containers:
                if container.get("name") == "agent":
                    container["args"] = ["-test.coverprofile=coverage.txt", "DEVEL"] + container["args"]
                    container_envars = container.get("env", [])
                    container_envars.append({
                        "name": "CODECOV_URL",
                        "value": os.environ.get("CODECOV_URL")
                    })

                    container["env"] = container_envars
                    container["image"] = AGET_IMAGE
                    del container["securityContext"]

        output_resources.append(resource)
    return output_resources


def query_constraints_by_names(session, account_id, names):
    body = {
        "filters": {
            "names": names,
            "enabled": True,
        }
    }
    resp = session.post(URL + f"/api/{account_id}/policies/v1/constraints/query", json=body)
    assert resp.ok, "failed to query constraints"
    return resp.json()["data"]


def query_templates_by_names(session, account_id, names):
    body = {
        "filters": {
            "names": names
        }
    }
    resp = session.post(URL + f"/api/{account_id}/policies/v1/templates/query", json=body)
    assert resp.ok, "failed to query templates"
    return resp.json()["data"]


def create_test_policy(session, account_id):
    templates = query_templates_by_names(session, account_id, ["Metadata Missing Label And Value"])
    body = templates[0]
    data = {
        "id": str(uuid.uuid4()),
        "name": "Test Policy " + uuid.uuid4().hex[:5],
        "category": body["category_id"],
        "enabled": True,
        "targets": {
            "cluster": [],
            "kind": [
                "Deployment",
                "ReplicaSet"
            ],
            "namespace": [],
            "label": {"test": "agent.integration.test"}
        },
        "parameters": [
            {
                "name": "label",
                "type": "string",
                "required": True,
                "default": "test-label",
            },
            {
                "name": "value",
                "type": "string",
                "required": True,
                "default": "test",
            },
            {
                "name": "exclude_namespace",
                "type": "string",
                "default": None,
                "required": False
            },
            {
                "name": "exclude_label_key",
                "type": "string",
                "default": None,
                "required": False
            },
            {
                "name": "exclude_label_value",
                "type": "string",
                "default": None,
                "required": False
            }
        ]
    }

    body.update(data)

    resp = session.post(URL + f"/api/{account_id}/policies/v1/policies", json=body)
    assert resp.ok, "Failed to create test policy"

    resp = session.get(URL + f"/api/{account_id}/policies/v1/policies/{resp.json()['id']}")
    assert resp.ok, "Failed to get created test policy"
    return body


def update_test_policy(session, account_id, policy):
    policy["parameters"] = [
        {
            "name": "label",
            "type": "string",
            "required": True,
            "default": "test-label-2",
        },
        {
            "name": "value",
            "type": "string",
            "required": True,
            "default": "test",
        },
        {
			"name": "exclude_namespace",
			"type": "string",
			"default": None,
			"required": False
		},
		{
			"name": "exclude_label_key",
			"type": "string",
			"default": None,
			"required": False
		},
		{
			"name": "exclude_label_value",
			"type": "string",
			"default": None,
			"required": False
		}
    ]

    resp = session.put(URL + f"/api/{account_id}/policies/v1/policies/{policy['id']}", json=policy)
    assert resp.ok, "Failed to update test policy"

def delete_test_policy(session, account_id, policy_id):
    resp = session.delete(URL + f"/api/{account_id}/policies/v1/constraints/{policy_id}")
    assert resp.ok, "Failed to delete test constraint"


def get_constraint_violation_count(session, account_id, cluster_id, constraint_id):
    body = {
        "filters": {
            "cluster_id":[cluster_id],
            "constraint_id":[constraint_id]
        },
        "limit": 100
    }

    resp = session.post(URL + f"/api/{account_id}/recommendations/v1/query", json=body)
    assert resp.ok, "Failed to get cluster recommendations"
    return resp.json()["count"]


class TestViolations:

    @pytest.fixture
    def prepare_env(self):
        os.system(f"kubectl create namespace {NAMESPACE}")

        exit_code = os.system(f"kubectl apply -f resources.yaml")
        assert exit_code == 0, "Failed to setup testing environment"

        yield

        exit_code = os.system(f"kubectl delete -f resources.yaml")
        assert exit_code == 0, "Failed to cleanup testing environment"

    @pytest.fixture
    def login(self, prepare_env):
        session = requests.Session()
        body = {"email": EMAIL, "password": PASSWORD}
        resp = session.post(URL + "/api/accounts/v1/public/login", json=body)
        assert resp.ok, "Login failed"
        account_id = resp.json()["account_id"]
        auth = resp.headers["authorization"]
        session.headers = {"authorization": auth}
        yield account_id, session

    @pytest.fixture
    def create_cluster(self, login):
        account_id, session = login
        test_constraints = query_constraints_by_names(session, account_id, TEST_CONSTRAINTS_NAMES)
        test_policy = create_test_policy(session, account_id)

        body = {"name": CLUSTER_NAME, "description": "agent integration test"}
        resp = session.post(URL + f"/api/accounts/v1/{account_id}/clusters", json=body)
        assert resp.ok, "Creating cluster failed"
        cluster_id = resp.json()["id"]

        resp = session.get(URL + f"/api/accounts/v1/{account_id}/clusters/{cluster_id}/url")
        assert resp.ok, "Failed to get cluster connect url"
        agent_yaml_url = resp.json()["url"]

        response = requests.get(agent_yaml_url)
        response.raise_for_status()

        agent_yaml = patch_agent_resources(response.text)
        with open(AGENT_YAML_PATH, "w") as f:
            yaml.dump_all(agent_yaml, f)

        exit_code = os.system(f"kubectl apply -f {AGENT_YAML_PATH}")
        assert exit_code == 0, "Failed to run agent create deployment command"

        yield account_id, cluster_id, session, test_policy, test_constraints

        exit_code = os.system(f"kubectl delete -f {AGENT_YAML_PATH}")
        assert exit_code == 0, "Failed to clean up agent deployment"

        resp = session.delete(URL + f"/api/accounts/v1/{account_id}/clusters/{cluster_id}")
        assert resp.ok, "Failed to delete cluster from console"

        try:
            delete_test_policy(session, account_id, test_policy["id"])
        except:
            pass

    def test_agent_violations(self, create_cluster):
        print("[+] Create cluster")
        account_id, cluster_id, session, test_policy, test_constraints = create_cluster
        time.sleep(180)

        print("[+] Check violations")
        for constraint in test_constraints:
            violations_count = get_constraint_violation_count(session, account_id, cluster_id, constraint["id"])
            assert violations_count == 1, "constraint: %s, expected 1 violation, but found %d" % (constraint["name"], violations_count)

        print("[+] Apply resources fixes")
        exit_code = os.system(f"kubectl apply -f fixed_resources.yaml")
        assert exit_code == 0, "Failed to apply fixed resources environment"

        time.sleep(180)

        print("[+] Check violations")
        for constraint in test_constraints:
            violations_count = get_constraint_violation_count(session, account_id, cluster_id, constraint["id"])
            assert violations_count == 0, "constraint: %s, expected 0 violations, but found %d" % (constraint["name"], violations_count)

        print("[+] Update test policy")
        update_test_policy(session, account_id, test_policy)
        time.sleep(180)

        print("[+] Check violations")
        violations_count = get_constraint_violation_count(session, account_id, cluster_id, test_policy["id"])
        assert violations_count == 1, f"expected 1 violations after updating test constraint, but found {violations_count}"

        print("[+] Delete test policy")
        delete_test_policy(session, account_id, test_policy["id"])
        time.sleep(180)

        print("[+] Check violations")
        violations_count = get_constraint_violation_count(session, account_id, cluster_id, test_policy["id"])
        assert violations_count == 0, f"expected 0 violations after deleting test constraint, but found {violations_count}"
