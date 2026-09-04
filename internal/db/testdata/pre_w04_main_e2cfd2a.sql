-- Immutable upgrade fixture captured from main@e2cfd2a before W04.
--
-- This fixture deliberately uses the legacy two-column schema_migrations
-- ledger and the 14-table schema that the application produced on MySQL 8.4.
-- Keep database names unqualified so the integration test can load it into an
-- isolated schema. Changes require an intentional checksum update in the test.

CREATE TABLE schema_migrations (
    version VARCHAR(128) NOT NULL PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE apps (
    app_id INT NOT NULL AUTO_INCREMENT,
    app_name VARCHAR(255) NOT NULL,
    rundeck_app_name VARCHAR(255) NULL,
    app_name_cn VARCHAR(255) NOT NULL,
    owner VARCHAR(100) NOT NULL,
    owner_cn VARCHAR(100) NOT NULL,
    dev_language VARCHAR(100) NOT NULL,
    description_cn VARCHAR(255) NULL,
    git_url VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (app_id)
) ENGINE=InnoDB AUTO_INCREMENT=10000 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE app_configs (
    config_id INT NOT NULL AUTO_INCREMENT,
    app_id INT NOT NULL,
    env VARCHAR(100) NOT NULL,
    active_env VARCHAR(100) GENERATED ALWAYS AS (IF(deleted_at IS NULL, env, NULL)) STORED,
    code_package_type VARCHAR(100) NOT NULL,
    code_package_path VARCHAR(255) NULL,
    code_package_name VARCHAR(255) NULL,
    base_image VARCHAR(255) NULL,
    pod_count INT DEFAULT 1,
    limits_memory INT DEFAULT 2,
    gpu_count INT DEFAULT 0,
    probe_type VARCHAR(100) DEFAULT 'TCP',
    probe_check_path VARCHAR(100) DEFAULT '/inside/checkup',
    probe_check_tcp_port INT NOT NULL DEFAULT 8080,
    probe_check_http_port INT NOT NULL DEFAULT 8080,
    probe_stop_check_http_port INT NOT NULL DEFAULT 8080,
    container_port INT NOT NULL DEFAULT 8080,
    pre_stop_type VARCHAR(100) DEFAULT 'TCP',
    pre_stop_check_path VARCHAR(100) DEFAULT '/inside/prestop',
    pre_stop_command VARCHAR(255) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (config_id),
    UNIQUE KEY uk_app_active_env (app_id, active_env),
    KEY IDX_app_configs_app_id (app_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE app_config_domains (
    id BIGINT NOT NULL AUTO_INCREMENT,
    config_id INT NOT NULL,
    host VARCHAR(255) NOT NULL,
    path VARCHAR(255) NOT NULL DEFAULT '/',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY UQE_app_config_domains_config_id_host_path (config_id, host, path),
    KEY IDX_app_config_domains_config_id (config_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE task_record (
    task_id INT NOT NULL AUTO_INCREMENT,
    app_name VARCHAR(255) NOT NULL,
    rundeck_app_name VARCHAR(255) NULL,
    branch VARCHAR(100) NOT NULL,
    env VARCHAR(255) NOT NULL,
    publisher VARCHAR(255) NOT NULL,
    ci_build_id INT DEFAULT 0,
    cd_build_id INT DEFAULT 0,
    pipeline_param JSON NULL,
    status VARCHAR(100) DEFAULT 'init',
    message VARCHAR(255) NULL,
    ci_job_name VARCHAR(100) NULL,
    cd_job_name VARCHAR(100) NULL,
    jenkins_address TEXT NULL,
    auto_deploy TINYINT(1) DEFAULT 1,
    products VARCHAR(255) NULL,
    engine_version INT NOT NULL DEFAULT 1,
    workflow_version_id BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (task_id),
    KEY idx_task_workflow_poll (engine_version, status, deleted_at, updated_at, task_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE task_record_images (
    id BIGINT NOT NULL AUTO_INCREMENT,
    task_id INT NOT NULL,
    img_type VARCHAR(32) NOT NULL,
    url VARCHAR(1024) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY UQE_task_record_images_task_id_img_type (task_id, img_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE pipelines (
    id INT NOT NULL AUTO_INCREMENT,
    job_name VARCHAR(100) NOT NULL,
    description_cn VARCHAR(255) NOT NULL,
    url VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY UQE_pipelines_job_name (job_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE pipelines_job_combination (
    id INT NOT NULL AUTO_INCREMENT,
    description_cn VARCHAR(255) NOT NULL,
    ci_job_name VARCHAR(100) NOT NULL,
    cd_job_name VARCHAR(100) NOT NULL,
    code_package_type VARCHAR(100) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY UQE_pipelines_job_combination_uk_ci_cd_combination (ci_job_name, cd_job_name),
    UNIQUE KEY UQE_pipelines_job_combination_code_package_type (code_package_type),
    KEY IDX_pipelines_job_combination_idx_ci_job (ci_job_name),
    KEY IDX_pipelines_job_combination_idx_cd_job (cd_job_name),
    CONSTRAINT fk_pipelines_ci_job FOREIGN KEY (ci_job_name) REFERENCES pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT fk_pipelines_cd_job FOREIGN KEY (cd_job_name) REFERENCES pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE env_configs (
    id INT NOT NULL AUTO_INCREMENT,
    env VARCHAR(100) NOT NULL,
    cluster_name VARCHAR(255) NULL,
    description_cn VARCHAR(255) NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    sort_order INT NOT NULL DEFAULT 0,
    harbor_url VARCHAR(255) NULL,
    harbor_project_name VARCHAR(255) NULL,
    node_version VARCHAR(255) NULL,
    maven_version VARCHAR(255) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY UQE_env_configs_env (env)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE integration_settings (
    provider VARCHAR(64) NOT NULL,
    config_data MEDIUMTEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE dev_language_rules (
    dev_language VARCHAR(100) NOT NULL,
    rules JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (dev_language)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE release_workflows (
    workflow_id BIGINT NOT NULL AUTO_INCREMENT,
    name VARCHAR(120) NOT NULL,
    description VARCHAR(500) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (workflow_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE release_workflow_versions (
    version_id BIGINT NOT NULL AUTO_INCREMENT,
    workflow_id BIGINT NOT NULL,
    version INT NOT NULL,
    spec JSON NOT NULL,
    checksum CHAR(64) NOT NULL,
    created_by VARCHAR(100) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (version_id),
    UNIQUE KEY uk_workflow_version (workflow_id, version),
    KEY idx_workflow_versions_workflow (workflow_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE app_config_workflows (
    binding_id BIGINT NOT NULL AUTO_INCREMENT,
    app_config_id INT NOT NULL,
    workflow_id BIGINT NOT NULL,
    version_id BIGINT NOT NULL,
    revision INT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (binding_id),
    UNIQUE KEY uk_app_config_workflow (app_config_id),
    KEY idx_app_config_workflow (workflow_id),
    KEY idx_app_config_workflow_version (version_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE task_step_records (
    step_record_id BIGINT NOT NULL AUTO_INCREMENT,
    task_id INT NOT NULL,
    workflow_version_id BIGINT NOT NULL,
    step_key VARCHAR(63) NOT NULL,
    name VARCHAR(120) NOT NULL,
    uses VARCHAR(120) NOT NULL,
    category VARCHAR(32) NULL,
    position INT NOT NULL,
    config JSON NOT NULL,
    timeout_seconds INT NOT NULL DEFAULT 3600,
    on_failure VARCHAR(16) NOT NULL DEFAULT 'stop',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempt INT NOT NULL DEFAULT 1,
    external_ref JSON NULL,
    output JSON NULL,
    message VARCHAR(1000) NULL,
    started_at TIMESTAMP NULL DEFAULT NULL,
    finished_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (step_record_id),
    UNIQUE KEY uk_task_step_key (task_id, step_key),
    KEY idx_task_position (task_id, position),
    KEY idx_task_status (task_id, status),
    KEY idx_task_workflow_version (workflow_version_id),
    KEY idx_step_status_uses (status, uses, task_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO dev_language_rules (dev_language, rules) VALUES
    ('java', '{"allowed":["jar","war"],"default":"jar"}'),
    ('python', '{"allowed":["python","ai"],"default":"python"}'),
    ('node.js', '{"allowed":["static","miniapp","node.js"],"default":"node.js"}'),
    ('golang', '{"allowed":["golang"],"default":"golang"}');

INSERT INTO apps (
    app_id, app_name, app_name_cn, owner, owner_cn, dev_language,
    description_cn, git_url
) VALUES (
    12345, 'fixture-api', '固定夹具应用', 'fixture-owner', '固定负责人', 'golang',
    '从 main@e2cfd2a 升级时必须保留', 'https://example.invalid/fixture-api.git'
);

INSERT INTO app_configs (
    config_id, app_id, env, code_package_type, code_package_path,
    code_package_name, base_image
) VALUES (
    23456, 12345, 'staging', 'golang', '/immutable/fixture',
    'fixture-api.tar.gz', 'golang:1.26'
);

INSERT INTO env_configs (
    id, env, cluster_name, description_cn, enabled, sort_order
) VALUES (
    34567, 'staging', 'fixture-cluster', '固定夹具环境', 1, 20
);

INSERT INTO schema_migrations (version, applied_at) VALUES
    ('20260902_001_cleanup_legacy_null_strings', '2026-09-03 01:01:01'),
    ('20260903_001_pluggable_cicd', '2026-09-03 01:02:02'),
    ('20260903_002_cicd_runtime_hardening', '2026-09-03 01:03:03');
