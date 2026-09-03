package publish

import (
	"fmt"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"strings"
)

// UpsertTaskAppletImagesRequest 任务图片写入请求
type UpsertTaskAppletImagesRequest struct {
	AppletImages []entity.AppletImage `json:"applet_images"`
}

// UpsertTaskAppletImages 覆盖写入指定 task_id 的图片列表（幂等：以 type 为维度）
func (pm *PublishManager) UpsertTaskAppletImages(taskID int, images []entity.AppletImage) error {
	if taskID <= 0 {
		return fmt.Errorf("无效的 task_id")
	}

	// 规范化与去重（按 type 去重，后者覆盖前者）
	byType := make(map[string]string, len(images))
	for _, img := range images {
		t := strings.TrimSpace(img.Type)
		u := strings.TrimSpace(img.URL)
		if t == "" || u == "" {
			continue
		}
		byType[t] = u
	}

	// 事务：先清空再写入，保证“覆盖写入”
	session := db.Engine.NewSession()
	defer session.Close()
	if err := session.Begin(); err != nil {
		return err
	}

	// 清空旧数据（硬删除，避免历史堆积）
	if _, err := session.Where("task_id = ?", taskID).Delete(&entity.TaskRecordImage{}); err != nil {
		_ = session.Rollback()
		return err
	}

	// 没有有效图片：清空即可
	if len(byType) == 0 {
		return session.Commit()
	}

	rows := make([]entity.TaskRecordImage, 0, len(byType))
	for t, u := range byType {
		rows = append(rows, entity.TaskRecordImage{
			TaskId:  taskID,
			ImgType: t,
			URL:     u,
		})
	}

	if _, err := session.Insert(&rows); err != nil {
		_ = session.Rollback()
		return err
	}

	return session.Commit()
}
