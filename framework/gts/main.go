package gts

import (
	"fmt"

	GTS "github.com/geewan-rd/GTS-go/v2/client/mobile"
)

func StartGTSWith(config string, fd int) string {
	return fmt.Sprintf("err:%s", GTS.StartWithFD(config, fd))
}
