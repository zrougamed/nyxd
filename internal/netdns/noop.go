package netdns

// Noop is a DNS backend that performs no registration or listening.
type Noop struct{}

func (Noop) Register(_, _ string) error { return nil }

func (Noop) Deregister(_ string) error { return nil }

func (Noop) Shutdown() {}

var _ Backend = Noop{}
