# Individual Resource Example

This is the direct-resource equivalent of `../basic/srl-multitool.yaml`. It defines the lab
without an auxiliary Topology resource:

- `00-node-profile.yaml` contains shared Kubernetes and node realization policy.
- `10-nodes.yaml` contains the SR Linux and network multitool Nodes.
- `20-link.yaml` connects `srl1:e1-1` to `multitool:eth1`.

Apply the complete example in one namespace:

```bash
kubectl create namespace srl-multitool
kubectl apply -n srl-multitool -f examples/individual-resources/
```

Inspect its resources:

```bash
kubectl get -n srl-multitool nodeprofiles,nodes.c9s.run,links
```

Delete the example:

```bash
kubectl delete namespace srl-multitool
```
