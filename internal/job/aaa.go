package job

import (
	"gitlab.ttpai.work/sre/pipeline/ares/internal/home"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/publish"
)

func init() {
	task := publish.NewTaskManager()
	Register("aaa", aaa)
	Register("taskManager", task.UpdateTaskStatuses)
}

func aaa() {
	home.HomeJob()
}
