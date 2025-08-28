package main

type shower interface {
	getWater() []shower
}

type display struct {
	SubDisplay *display
}

func (d display) getWater() []shower {
	return []shower{display{}, d.SubDisplay}
}

func main() {
	// SubDisplay will be initialized with null
	s := display{}
	water := s.getWater()
	for _, x := range water {
		if x == nil {
			panic("everything ok, nil found")
		}

		// First iteration display{} is not nil and will
		// therefore work, on the second iteration
		// x is nil, and getWater panics.
		x.getWater()
	}
}
