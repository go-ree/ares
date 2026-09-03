CREATE TABLE IF NOT EXISTS ares.schema_migrations (
    version VARCHAR(128) NOT NULL PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

# 创建apps表，用来存储app信息
CREATE TABLE IF NOT EXISTS ares.apps (
    app_id INT(11) AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    rundeck_app_name VARCHAR(255) DEFAULT null,
    app_name_cn VARCHAR(255) NOT NULL,
    owner VARCHAR(100) NOT NULL,
    owner_cn varchar(100) NOT NULL,
    dev_language VARCHAR(100) NOT NULL,
    description_cn varchar(255) DEFAULT NULL,
    git_url varchar(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
)AUTO_INCREMENT = 10000;

# 创建app_config表，用来存储发布信息
CREATE TABLE IF NOT EXISTS ares.app_configs (
    config_id  INT(11)  AUTO_INCREMENT PRIMARY KEY,
    app_id     INT(11)  NOT NULL,
    env        VARCHAR(100) NOT NULL,
    code_package_type VARCHAR(100) NOT NULL,
    code_package_path VARCHAR(255) DEFAULT NULL,
    code_package_name VARCHAR(255) DEFAULT NULL,
    base_image  VARCHAR(255) DEFAULT NULL,

    # 运行时配置
    pod_count INT(11) DEFAULT 1,
    # 这里规定只能使用Gi
    limits_memory INT(11) DEFAULT 2,
    gpu_count   INT(11) DEFAULT 0,
    probe_type  VARCHAR(100) DEFAULT 'TCP',
    probe_check_path VARCHAR(100) DEFAULT '/inside/checkup',
    probe_check_tcp_port INT(11) NOT NULL DEFAULT 8080,
    probe_check_http_port INT(11) NOT NULL DEFAULT 8080,
    probe_stop_check_http_port INT(11) NOT NULL DEFAULT 8080,
    container_port INT(11) NOT NULL DEFAULT 8080,
    pre_stop_type VARCHAR(100) DEFAULT 'TCP',
    pre_stop_check_path VARCHAR(100) DEFAULT '/inside/prestop',
    pre_stop_command VARCHAR(255) DEFAULT NULL,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    active_env VARCHAR(100) GENERATED ALWAYS AS (IF(deleted_at IS NULL, env, NULL)) STORED,
    INDEX idx_app_id (app_id),
    UNIQUE INDEX uk_app_active_env (app_id, active_env),
    FOREIGN KEY (app_id) REFERENCES apps(app_id)
);

-- 多域名配置：基于 app_configs.config_id 绑定多个 host/path
CREATE TABLE IF NOT EXISTS ares.app_config_domains (
    id BIGINT(20) AUTO_INCREMENT PRIMARY KEY,
    config_id INT(11) NOT NULL,
    host VARCHAR(255) NOT NULL,
    path VARCHAR(255) NOT NULL DEFAULT '/',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    UNIQUE KEY uk_config_id_host_path (config_id, host, path),
    INDEX idx_config_id (config_id)
);

CREATE TABLE IF NOT EXISTS ares.task_record (
    task_id INT(11) AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    rundeck_app_name VARCHAR(255) DEFAULT null,
    branch VARCHAR(100) NOT NULL,
    publisher VARCHAR(255) NOT NULL,
    env     varchar(100) NOT NULL,

    # job流程组合
    ci_build_id INT(11) DEFAULT 0,
    cd_build_id INT(11) DEFAULT 0,
    pipeline_param json,
    # init(初始状态)、running（运行中）、success（成功）、failed（失败）
    status VARCHAR(100) DEFAULT 'init',
    message VARCHAR(255) DEFAULT NULL,
    ci_job_name VARCHAR(100) DEFAULT NULL,
    cd_job_name VARCHAR(100) DEFAULT NULL,
	jenkins_address TEXT DEFAULT NULL,
    # 自动触发cd阶段
    auto_deploy TINYINT(1) DEFAULT 1 COMMENT '0 for false, 1 for true',

    # 产出物
    products varchar(255) DEFAULT NULL,
    engine_version INT NOT NULL DEFAULT 1,
    workflow_version_id BIGINT NOT NULL DEFAULT 0,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	deleted_at TIMESTAMP NULL DEFAULT NULL,
	INDEX idx_task_workflow_poll (engine_version, status, deleted_at, updated_at, task_id)
);

-- 任务图片表：按 task_id 存放多种渠道（type）的图片 url
CREATE TABLE IF NOT EXISTS ares.task_record_images (
    id BIGINT(20) AUTO_INCREMENT PRIMARY KEY,
    task_id INT(11) NOT NULL,
    img_type VARCHAR(32) NOT NULL,
    url VARCHAR(1024) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_task_id_img_type (task_id, img_type),
    INDEX idx_task_id (task_id)
);

CREATE TABLE IF NOT EXISTS ares.pipelines (
    id INT(11) AUTO_INCREMENT PRIMARY KEY,
    job_name VARCHAR(100) NOT NULL UNIQUE,
    description_cn VARCHAR(255) NOT NULL,
    url VARCHAR(255) NOT NULL,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS ares.pipelines_job_combination (
    id INT(11) AUTO_INCREMENT PRIMARY KEY,
    description_cn VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
    -- 与pipelines.job_name完全匹配的字段定义
    ci_job_name VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
    cd_job_name VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
    code_package_type VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL UNIQUE,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,

    INDEX idx_cd_job (cd_job_name),
    INDEX idx_ci_job (ci_job_name),

    -- 外键约束（字段类型完全兼容）
    CONSTRAINT fk_pipelines_ci_job
    FOREIGN KEY (ci_job_name)
    REFERENCES ares.pipelines(job_name)
    ON DELETE RESTRICT
    ON UPDATE CASCADE,

    CONSTRAINT fk_pipelines_cd_job
    FOREIGN KEY (cd_job_name)
    REFERENCES ares.pipelines(job_name)
    ON DELETE RESTRICT
    ON UPDATE CASCADE,

    UNIQUE INDEX uk_ci_cd_combination (ci_job_name, cd_job_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
#######################################以上均为投产版##########################################################
# 创建env表，用来存储环境对应集群信息
CREATE TABLE IF NOT EXISTS ares.env_configs (
    id INT(11) AUTO_INCREMENT PRIMARY KEY,
    env VARCHAR(100) NOT NULL UNIQUE,
    cluster_name VARCHAR(255) DEFAULT NULL,
    description_cn varchar(255) NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    sort_order INT NOT NULL DEFAULT 0,
    harbor_url  VARCHAR(255) DEFAULT NULL,
    harbor_project_name VARCHAR(255) DEFAULT NULL,
    node_version VARCHAR(255) DEFAULT NULL,
    maven_version VARCHAR(255) DEFAULT NULL,



    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- 可插拔发布流程：流程身份、不可变版本、应用环境绑定与任务步骤快照
CREATE TABLE IF NOT EXISTS ares.release_workflows (
    workflow_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    description VARCHAR(500) DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS ares.release_workflow_versions (
    version_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    workflow_id BIGINT NOT NULL,
    version INT NOT NULL,
    spec JSON NOT NULL,
    checksum CHAR(64) NOT NULL,
    created_by VARCHAR(100) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_workflow_version (workflow_id, version),
    INDEX idx_workflow_versions_workflow (workflow_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS ares.app_config_workflows (
    binding_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_config_id INT NOT NULL,
    workflow_id BIGINT NOT NULL,
    version_id BIGINT NOT NULL,
    revision INT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_app_config_workflow (app_config_id),
    INDEX idx_app_config_workflow (workflow_id),
    INDEX idx_app_config_workflow_version (version_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS ares.task_step_records (
    step_record_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    task_id INT NOT NULL,
    workflow_version_id BIGINT NOT NULL,
    step_key VARCHAR(63) NOT NULL,
    name VARCHAR(120) NOT NULL,
    uses VARCHAR(120) NOT NULL,
    category VARCHAR(32) DEFAULT NULL,
    position INT NOT NULL,
    config JSON NOT NULL,
    timeout_seconds INT NOT NULL DEFAULT 3600,
    on_failure VARCHAR(16) NOT NULL DEFAULT 'stop',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempt INT NOT NULL DEFAULT 1,
    external_ref JSON NULL,
    output JSON NULL,
    message VARCHAR(1000) DEFAULT NULL,
    started_at TIMESTAMP NULL DEFAULT NULL,
    finished_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_task_step_key (task_id, step_key),
    INDEX idx_task_position (task_id, position),
    INDEX idx_task_status (task_id, status),
	INDEX idx_step_status_uses (status, uses, task_id),
    INDEX idx_task_workflow_version (workflow_version_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- dev_language 与 code_package_type 规则（单表 JSON）
-- rules 示例：{"allowed":["jar","war"],"default":"jar"}
CREATE TABLE IF NOT EXISTS ares.dev_language_rules (
    dev_language VARCHAR(100) PRIMARY KEY COMMENT '与 apps.dev_language 一致',
    rules JSON NOT NULL COMMENT '{"allowed":[...],"default":"..."}',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    CHECK (JSON_VALID(rules))
);

INSERT IGNORE INTO ares.dev_language_rules (dev_language, rules) VALUES
('java',    JSON_OBJECT('allowed', JSON_ARRAY('jar','war'),                  'default','jar')),
('python',  JSON_OBJECT('allowed', JSON_ARRAY('python','ai'),                'default','python')),
('node.js', JSON_OBJECT('allowed', JSON_ARRAY('static','miniapp','node.js'), 'default','node.js')),
('golang',  JSON_OBJECT('allowed', JSON_ARRAY('golang'),                     'default','golang'));

#######################################以上均为预投产版##########################################################
