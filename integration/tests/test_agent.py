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
CATEGORY_ID = "magalix.categories.none"


TEST_CONSTRAINTS_NAMES = [
    "Using latest Image Tag",
    "Services are not using ports over 1024",
    "Missing Owner Label",
    "Containers running with PrivilegeEscalation"
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
        "category": CATEGORY_ID,
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
    ],
    }


    body.update(data)

    resp = session.post(URL + f"/api/{account_id}/policies/v1/policies", json=body)
    assert resp.ok, "Failed to create test policy"

    resp = session.get(URL + f"/api/{account_id}/policies/v1/policies/{resp.json()['id']}")
    assert resp.ok, "Failed to get created test policy"
    return resp.json()


def update_test_policy(session, account_id, test_policy):
    test_policy["parameters"] = [
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
    ]

    resp = session.put(URL + f"/api/{account_id}/policies/v1/policies/{test_policy['id']}", json=test_policy)
    assert resp.ok, "Failed to update test policy"

def delete_test_policy(session, account_id, test_policy):
    resp = session.delete(URL + f"/api/{account_id}/policies/v1/constraints/{test_policy['id']}")
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

        time.sleep(360)

        yield account_id, cluster_id, session, test_policy, test_constraints

        exit_code = os.system(f"kubectl delete -f {AGENT_YAML_PATH}")
        assert exit_code == 0, "Failed to clean up agent deployment"

        # resp = session.delete(URL + f"/api/accounts/v1/{account_id}/clusters/{cluster_id}")
        # assert resp.ok, "Failed to delete cluster from console"

        try:
            delete_test_policy(session, account_id, test_policy)
        except:
            pass

    def test_agent_violations(self, create_cluster):
        account_id, cluster_id, session, test_policy, test_constraints = create_cluster

        for constraint in test_constraints:
            violations_count = get_constraint_violation_count(session, account_id, cluster_id, constraint["id"])
            assert violations_count == 1, "constraint: %s, expected 1 violation, but found %d" % (constraint["name"], violations_count)


        exit_code = os.system(f"kubectl apply -f fixed_resources.yaml")
        assert exit_code == 0, "Failed to apply fixed resources environment"

        time.sleep(200)

        for constraint in test_constraints:
            violations_count = get_constraint_violation_count(session, account_id, cluster_id, constraint["id"])
            assert violations_count == 0, "constraint: %s, expected 0 violations, but found %d" % (constraint["name"], violations_count)

        update_test_policy(session, account_id, test_policy)
        time.sleep(200)

        violations_count = get_constraint_violation_count(session, account_id, cluster_id, test_policy["id"])
        assert violations_count == 1, f"expected 1 violations after updating test constraint, but found {violations_count}"


        delete_test_policy(session, account_id, test_policy)
        time.sleep(200)

        violations_count = get_constraint_violation_count(session, account_id, cluster_id, test_policy["id"])
        assert violations_count == 0, f"expected 0 violations after deleting test constraint, but found {violations_count}"
