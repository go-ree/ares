# 创建apps表，用来存储app信息
CREATE TABLE ares.apps (
    app_id INT(11) AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
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

# 创建job表，用来存储job信息
CREATE TABLE ares.jenkins_jobs (
                                   id INT(11) AUTO_INCREMENT PRIMARY KEY,
                                   job_name VARCHAR(255) NOT NULL UNIQUE,
                                   description_cn varchar(255) NOT NULL,

                                   created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                                   updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                                   deleted_at TIMESTAMP NULL DEFAULT NULL
);


# 创建env表，用来存储环境对应集群信息
CREATE TABLE ares.env (
                          id INT(11) AUTO_INCREMENT PRIMARY KEY,
                          env VARCHAR(100) NOT NULL UNIQUE,
                          cluster_name VARCHAR(255) NOT NULL,
                          description_cn varchar(255) NOT NULL,


                          created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                          updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                          deleted_at TIMESTAMP NULL DEFAULT NULL
);
CREATE TABLE ares.publish (
    id INT(11) AUTO_INCREMENT PRIMARY KEY,


    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

CREATE TABLE ares.task_record (
    task_id INT(11) AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,

    # 打包中（packaging）、打包成功（packaged）、部署中（deploying）、部署成功（deployed)、打包失败（package_failed）、部署失败（deploy_failed）
    # job_name的执行结果。示例：java-jar应用打包 失败
    message varchar(255) NOT NULL,

    # 当前发布id
    aaa INT(11),

    # job流程组合
    stage_id_str varchar(100) NOT NULL,
    # jenkins中的job任务的执行记录（所有内容应均为string）
    job_task_info json DEFAULT NULL,

    # jenkins相关参数信息（所有内容应均为string）
    param json DEFAULT NULL,

    # 产出物
    products varchar(255) DEFAULT NULL,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);






# 创建apps表，用来存储app信息（临时存储表，把一些基础信息也加上）
CREATE TABLE ares.tmp_apps (
    app_id INT(11) AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    app_name_cn VARCHAR(255) DEFAULT 'NULL',
    owner VARCHAR(100) NOT NULL,
    owner_cn varchar(100) NOT NULL,
    dev_language VARCHAR(100) NOT NULL,
    description_cn varchar(255) DEFAULT 'NULL',
    git_url varchar(255) NOT NULL,

    code_package_type VARCHAR(100) DEFAULT 'NULL',
    code_package_path VARCHAR(255) DEFAULT 'NULL',
    base_image  VARCHAR(255) DEFAULT 'NULL',

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
)AUTO_INCREMENT = 10000;