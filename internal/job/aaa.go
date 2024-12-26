package job

import (
	"gitlab.ttpai.work/sre/pipeline/ares/internal/home"
)

func init() {
	Register("aaa", aaa)
}

func aaa() {
	home.HomeJob()
}
