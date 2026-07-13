package constants

const (
	// AnnotationLinkAttachmentsDigest is the annotation set on launcher deployment pod templates
	// holding a digest of the node's local link attachment points (the interfaces the launcher
	// creates host side veths for). Adding/removing a link (or changing its mtu) changes the
	// digest, which rolls the pod so the launcher can redeploy the topology with the correct
	// attachment set -- while remote-side-only link changes (rewires) leave the digest (and the
	// pod) alone and are handled live by the launcher's link watch.
	AnnotationLinkAttachmentsDigest = "clabernetes/linkAttachmentsDigest"
)
