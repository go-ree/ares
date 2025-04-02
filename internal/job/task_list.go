package job

import (
	"ares/internal/publish"
)

func init() {
	task := publish.NewTaskManager()
	Register("taskManager", task.UpdateTaskStatuses)
}
