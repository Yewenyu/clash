package patch

import (
	C "github.com/Dreamacro/clash/constant"
)

func SourceValid(m *C.Metadata) bool {
	return m.SrcPort != 0
	//return len(m.SrcPort) > 0 && strings.Compare(m.SrcPort, "0") != 0 // && m.SrcIP.IsValid()
}
