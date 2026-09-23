"""Run with: python3 -m unittest discover -s hack -p 'test_monitor_topology_startup.py'."""

import unittest

from monitor_topology_startup import summarize


class StartupSummaryTest(unittest.TestCase):
    def test_primary_startup_is_independent_of_helper_and_pod_readiness(self):
        def pod(uid, grant, statuses):
            return {
                "metadata": {"uid": uid, "annotations": {
                    "kubectl.kubernetes.io/default-container": "device",
                    "c9s.run/startup-host-admitted": grant,
                }},
                "spec": {"nodeName": "worker", "initContainers": [{"name": "startup-gate"}]},
                "status": {"containerStatuses": statuses, "conditions": [{"type": "Ready", "status": "False"}]},
            }
        pods = {
            "queued": pod("new-pod", "previous-pod", []),
            "booting": pod("booting", "booting", [{"name": "helper", "started": True}]),
            "started": pod("started", "started", [{"name": "device", "started": True}]),
        }
        self.assertEqual(summarize(pods), {"worker": dict(pods=3, queued=1, booting=1, started=1, ready=0)})


if __name__ == "__main__":
    unittest.main()
