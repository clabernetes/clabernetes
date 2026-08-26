# documentation-site — Delta Specification

## REMOVED Requirements

### Requirement: Custom containerd registry hosts configuration is documented

**Reason**: The pull-through image path and its `criHostsDir` Config field were removed with
the launcher runtime; there is no containerd hosts-directory mount left to document. The
kubelet performs the only image pull.
