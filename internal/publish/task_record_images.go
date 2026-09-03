package publish

import (
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
)

// fetchTaskRecordImagesByTaskIDs 批量查询任务图片并按 task_id 聚合
func fetchTaskRecordImagesByTaskIDs(taskIDs []int) (map[int][]entity.AppletImage, error) {
	result := make(map[int][]entity.AppletImage, len(taskIDs))
	if len(taskIDs) == 0 {
		return result, nil
	}

	var rows []entity.TaskRecordImage
	if err := db.Engine.In("task_id", taskIDs).Find(&rows); err != nil {
		return nil, err
	}

	for _, r := range rows {
		result[r.TaskId] = append(result[r.TaskId], entity.AppletImage{
			Type: r.ImgType,
			URL:  r.URL,
		})
	}

	return result, nil
}
