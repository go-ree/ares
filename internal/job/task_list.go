package job

import (
	"github.com/go-ree/ares/internal/publish"
)

func init() {
	task := publish.NewTaskManager()
	Register("taskManager", task.UpdateTaskStatuses)
}
