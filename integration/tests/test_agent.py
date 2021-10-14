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

TEST_CONSTRAINTS_1 = [
    {
        "id": "584c046b-bb1f-4f48-a10e-69c1de7511d3",
        "name": "Using latest Image Tag",
        "violations": 1
    },
    {
        "id": "bea92907-dc7f-406f-9012-21de7653925a",
        "name": "Services are not using ports over 1024",
        "violations": 1
    },
    {
        "id": "8e16cb95-047e-4aed-95b4-ea7a0d89141f",
        "name": "Missing Owner Label",
        "violations": 1
    },
    {
        "id": "4e65028c-1988-4f91-9328-23953352037d",
        "name": "Containers running with PrivilegeEscalation",
        "violations": 1
    }
]

TEST_CONSTRAINTS_2 = [
    {
        "id": "584c046b-bb1f-4f48-a10e-69c1de7511d3",
        "name": "Using latest Image Tag",
        "violations": 0
    },
    {
        "id": "bea92907-dc7f-406f-9012-21de7653925a",
        "name": "Services are not using ports over 1024",
        "violations": 0
    },
    {
        "id": "8e16cb95-047e-4aed-95b4-ea7a0d89141f",
        "name": "Missing Owner Label",
        "violations": 0
    },
    {
        "id": "4e65028c-1988-4f91-9328-23953352037d",
        "name": "Containers running with PrivilegeEscalation",
        "violations": 0
    }
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
        resp = session.post(URL + "/api/v0.1/login/standard", json=body)
        assert resp.ok, "Login failed"
        account_id = resp.json()["account_id"]
        auth = resp.headers["authorization"]
        session.headers = {"authorization": auth}
        yield account_id, session

    @pytest.fixture
    def create_cluster(self, login):
        account_id, session = login

        body = {"name": CLUSTER_NAME, "description": "agent integration test"}
        resp = session.post(URL + f"/api/v0.1/accounts/{account_id}/clusters", json=body)
        assert resp.ok, "Creating cluster failed"
        cluster_id = resp.json()["id"]

        resp = session.get(URL + f"/api/v0.1/accounts/{account_id}/clusters/{cluster_id}/url")
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
    
        yield account_id, cluster_id, session

        exit_code = os.system(f"kubectl delete -f {AGENT_YAML_PATH}")
        assert exit_code == 0, "Failed to clean up agent deployment"

        resp = session.delete(URL + f"/api/v0.1/accounts/{account_id}/clusters/{cluster_id}")
        assert resp.ok, "Failed to delete cluster from console"

    def test_agent_violations(self, create_cluster):
        account_id, cluster_id, session = create_cluster

        for constraint in TEST_CONSTRAINTS_1:
            body = {
                "filters": {
                    "cluster_id":[cluster_id],
                    "constraint_id":[constraint["id"]]
                },
                "limit": 100
            }

            resp = session.post(URL + f"/api/{account_id}/recommendations/v1/query", json=body)
            assert resp.ok, "Failed to get cluster recommendations"

            violations_count = resp.json()["count"] 
            assert violations_count == constraint["violations"], "constraint: %s, expected %d violations, but found %d" % (constraint["name"], constraint["violations"], violations_count)


        exit_code = os.system(f"kubectl apply -f fixed_resources.yaml")
        assert exit_code == 0, "Failed to apply fixed resources environment"

        time.sleep(360)

        for constraint in TEST_CONSTRAINTS_2:
            body = {
                "filters": {
                    "cluster_id":[cluster_id],
                    "constraint_id":[constraint["id"]]
                },
                "limit": 100
            }

            resp = session.post(URL + f"/api/{account_id}/recommendations/v1/query", json=body)
            assert resp.ok, "Failed to get cluster recommendations"

            violations_count = resp.json()["count"] 
            assert violations_count == constraint["violations"], "constraint: %s, expected %d violations, but found %d" % (constraint["name"], constraint["violations"], violations_count)
