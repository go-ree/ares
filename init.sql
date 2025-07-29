# 创建apps表，用来存储app信息
CREATE TABLE ares.apps (
    app_id INT(11) AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    rundeck_app_name VARCHAR(255) DEFAULT null,
    app_name_cn VARCHAR(255) DEFAULT 'NULL',
    owner VARCHAR(100) NOT NULL,
    owner_cn varchar(100) NOT NULL,
    dev_language VARCHAR(100) NOT NULL,
    description_cn varchar(255) DEFAULT 'NULL',
    git_url varchar(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
)AUTO_INCREMENT = 10000;

# 创建app_config表，用来存储发布信息
CREATE TABLE ares.app_configs (
    config_id  INT(11)  AUTO_INCREMENT PRIMARY KEY,
    app_id     INT(11)  NOT NULL,
    env        VARCHAR(100) NOT NULL,
    code_package_type VARCHAR(100) DEFAULT 'NULL',
    code_package_path VARCHAR(255) DEFAULT 'NULL',
    code_package_name VARCHAR(255) DEFAULT 'NULL',
    base_image  VARCHAR(255) DEFAULT 'NULL',

    # 运行时配置
    pod_count INT(11) DEFAULT 1,
    # 这里规定只能使用Gi
    limits_memory INT(11) DEFAULT 2,
    gpu_count   INT(11) DEFAULT 1,
    probe_type  VARCHAR(100) DEFAULT 'TCP',
    probe_check_path VARCHAR(100) DEFAULT '/ttpai/inside/checkup',
    pre_stop_type VARCHAR(100) DEFAULT 'TCP',
    pre_stop_check_path VARCHAR(100) DEFAULT '/ttpai/inside/prestop',
    pre_stop_command VARCHAR(255) DEFAULT 'NULL',
    domain VARCHAR(255) DEFAULT 'NULL',
    domain_path VARCHAR(255) DEFAULT '/',

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    INDEX idx_app_id (app_id),
    FOREIGN KEY (app_id) REFERENCES apps(app_id)
);

CREATE TABLE ares.task_record (
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
    message VARCHAR(255) DEFAULT 'NULL',
    ci_job_name VARCHAR(100) DEFAULT 'NULL',
    cd_job_name VARCHAR(100) DEFAULT 'NULL',
    # 自动触发cd阶段
    auto_deploy TINYINT(1) DEFAULT 1 COMMENT '0 for false, 1 for true',

    # 产出物
    products varchar(255) DEFAULT 'NULL',

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

CREATE TABLE ares.pipelines (
    id INT(11) AUTO_INCREMENT PRIMARY KEY,
    job_name VARCHAR(100) NOT NULL UNIQUE,
    description_cn VARCHAR(255) NOT NULL,
    code_package_type VARCHAR(100) NOT NULL UNIQUE,
    url VARCHAR(255) NOT NULL,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);
#######################################以上均为投产版##########################################################
# 创建env表，用来存储环境对应集群信息
CREATE TABLE ares.env_configs (
    id INT(11) AUTO_INCREMENT PRIMARY KEY,
    env VARCHAR(100) NOT NULL UNIQUE,
    cluster_name VARCHAR(255) NOT NULL,
    description_cn varchar(255) NOT NULL,
    harbor_url  VARCHAR(255) NOT NULL,
    harbor_project_name VARCHAR(255) NOT NULL,
    node_version VARCHAR(255) NOT NULL,
    maven_version VARCHAR(255) NOT NULL,



    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

#######################################以上均为预投产版##########################################################