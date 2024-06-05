package outbound

type (
	VlessV0       = Vmess
	VlessOptionV0 = VmessOption
)

func NewVlessV0(option VlessOptionV0) (*VlessV0, error) {
	return newVmess(option, true)
}
