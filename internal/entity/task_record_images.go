package entity

import "time"

// AppletImage 对外返回的小程序图片信息
type AppletImage struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// TaskRecordImage 任务记录对应的图片资源（按 task_id 聚合）
type TaskRecordImage struct {
	ID      int64  `xorm:"BIGINT(20) pk autoincr 'id'" json:"-"`
	TaskId  int    `xorm:"INT(11) notnull unique(task_id_img_type) 'task_id'" json:"-"`
	ImgType string `xorm:"VARCHAR(32) notnull unique(task_id_img_type) 'img_type'" json:"-"`
	URL     string `xorm:"VARCHAR(1024) notnull 'url'" json:"-"`

	CreatedTime time.Time `xorm:"timestamp created notnull DEFAULT CURRENT_TIMESTAMP 'created_at'" json:"-"`
	UpdatedTime time.Time `xorm:"timestamp updated notnull DEFAULT CURRENT_TIMESTAMP 'updated_at'" json:"-"`
}

func (TaskRecordImage) TableName() string {
	return "task_record_images"
}
