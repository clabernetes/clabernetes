package bar

var kindNames = []string{"bar"}

func Register(r *Registry) {
	r.Register(kindNames, nil, nil)
}
