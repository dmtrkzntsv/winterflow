package mode

type AppMode string

const (
	AppModeStandalone  AppMode = "standalone"
	AppModeDistributed AppMode = "distributed"
)

func (m AppMode) String() string {
	return string(m)
}
