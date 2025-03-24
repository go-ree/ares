package job

import (
	"ares/internal/home"
	"ares/internal/publish"
)

func init() {
	task := publish.NewTaskManager()
	Register("aaa", aaa)
	Register("taskManager", task.UpdateTaskStatuses)
}

func aaa() {
	home.HomeJob()
}
