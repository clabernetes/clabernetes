package foo

const canonical = "foo"

var kindNames = []string{canonical, "foo_alias"}

func Register(r *Registry) {
	r.Register(kindNames, nil, nil)
}
