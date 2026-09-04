#!/usr/bin/env bash

set -Eeuo pipefail

fail() {
	printf 'Ares MySQL 账号动态集成测试失败：%s\n' "$1" >&2
	exit 1
}

require_value() {
	local name="$1"
	[[ -n "${!name:-}" ]] || fail "环境变量 ${name} 不能为空"
}

require_value ARES_TEST_MYSQL_CONTAINER
require_value ARES_TEST_MYSQL_ROOT_PASSWORD

docker_bin="${DOCKER_BIN:-docker}"
command -v "$docker_bin" >/dev/null 2>&1 || fail "找不到 Docker 命令：${docker_bin}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
account_init_script="${script_dir}/01-create-users.sh"
[[ -r "$account_init_script" ]] || fail "找不到账号初始化脚本：${account_init_script}"

test_container="$ARES_TEST_MYSQL_CONTAINER"
root_password="$ARES_TEST_MYSQL_ROOT_PASSWORD"
if ! "$docker_bin" inspect "$test_container" >/dev/null 2>&1; then
	fail "MySQL 测试容器不存在：${test_container}"
fi

mysql_root() {
	local statement="$1"
	"$docker_bin" exec \
		--env "MYSQL_PWD=${root_password}" \
		"$test_container" \
		mysql \
		--protocol=tcp \
		--host=127.0.0.1 \
		--user=root \
		--connect-timeout=5 \
		--skip-reconnect \
		--binary-mode \
		--skip-commands \
		--batch \
		--skip-column-names \
		--raw \
		--execute="$statement"
}

mysql_as_user() {
	local user="$1"
	local password="$2"
	local database="$3"
	local statement="$4"
	"$docker_bin" exec \
		--env "MYSQL_PWD=${password}" \
		"$test_container" \
		mysql \
		--protocol=tcp \
		--host=127.0.0.1 \
		--user="$user" \
		--database="$database" \
		--connect-timeout=5 \
		--batch \
		--skip-column-names \
		--raw \
		--execute="$statement"
}

run_account_init() {
	local role="$1"
	local database="$2"
	local migration_user="$3"
	local migration_password="$4"
	local runtime_user="$5"
	local runtime_password="$6"
	local lock_timeout="${7:-10}"
	local selected_root_password="${8:-$root_password}"

	"$docker_bin" exec -i \
		--env "ARES_DATABASE_ACCOUNT_ROLE=${role}" \
		--env ARES_DATABASE_ACCOUNT_CONNECT_TIMEOUT_SECONDS=5 \
		--env ARES_DATABASE_ACCOUNT_INIT_TIMEOUT_SECONDS=60 \
		--env "ARES_DATABASE_ACCOUNT_LOCK_TIMEOUT_SECONDS=${lock_timeout}" \
		--env MYSQL_HOST=127.0.0.1 \
		--env "MYSQL_DATABASE=${database}" \
		--env "MYSQL_ROOT_PASSWORD=${selected_root_password}" \
		--env "MYSQL_MIGRATION_USER=${migration_user}" \
		--env "MYSQL_MIGRATION_PASSWORD=${migration_password}" \
		--env "MYSQL_RUNTIME_USER=${runtime_user}" \
		--env "MYSQL_RUNTIME_PASSWORD=${runtime_password}" \
		"$test_container" bash -s < "$account_init_script"
}

mysql_socket_root() {
	local statement="$1"
	"$docker_bin" exec \
		--env "MYSQL_PWD=${root_password}" \
		"$test_container" \
		mysql --protocol=socket --user=root --connect-timeout=5 \
		--batch --skip-column-names --raw --execute="$statement"
}

assert_query_equals() {
	local expected="$1"
	local statement="$2"
	local description="$3"
	local actual
	actual="$(mysql_root "$statement")" || fail "${description}：查询失败"
	[[ "$actual" == "$expected" ]] || fail \
		"${description}：期望 [${expected}]，实际 [${actual}]"
}

hex_upper() {
	LC_ALL=C od -An -tx1 | tr -d ' \n' | tr '[:lower:]' '[:upper:]'
}

suffix="$(printf '%x%04x' "$$" "$RANDOM")"
database="ares_w04_${suffix}"
decoy_database="${database//_/X}"
external_database="ares_w04_external_${suffix}"
cross_database="ares_w04_cross_${suffix}"
capability_database="ares_w04_cap_${suffix}"
case_database="ares_w04_case_${suffix}"
blocked_database="ares_w04_block_${suffix}"
blocked_grant_pattern="${blocked_database//_/\\_}"
lock_database="ares_w04_lock_${suffix}"
migration_user="w04_m_${suffix}"
runtime_user="w04_r_${suffix}"
legacy_user="w04_old_${suffix}"
blocked_migration_user="w04_bm_${suffix}"
blocked_runtime_user="w04_br_${suffix}"
lock_migration_user="w04_lm_${suffix}"
lock_runtime_user="w04_lr_${suffix}"
unsafe_runtime_user="w04_ur_${suffix}"
legacy_pattern_user="w04_lp_${suffix}"
cross_user_a="w04_ca_${suffix}"
cross_user_b="w04_cb_${suffix}"
capability_migration_user="w04_pm_${suffix}"
capability_runtime_user="w04_pr_${suffix}"
case_migration_user="W04CaseM${suffix}"
case_variant_user="$(printf '%s' "$case_migration_user" | tr '[:upper:]' '[:lower:]')"
case_runtime_user="W04CaseR${suffix}"
case_password="Aa1!case-${suffix}"
restricted_root_password="Aa1!restricted-root-${suffix}"
failure_role_prefix="w04_role_${suffix}"
special_proxy_user="w04_proxy_${suffix}'x"
special_proxy_user_sql="$(printf '%s' "$special_proxy_user" | LC_ALL=C sed "s/'/''/g")"
migration_marker="W04MigrationSecret${suffix}"
runtime_marker="W04RuntimeSecret${suffix}"
migration_password="m-${migration_marker}-slash\\semi;cmd\\c-quo'te-dollar\$!"
runtime_password="r-${runtime_marker}-slash\\semi;cmd\\c-quo'te-dollar\$!"
[[ "$migration_password" == *'\c'* && "$migration_password" == *\\* && \
	"$migration_password" == *';'* && "$migration_password" == *"'"* && \
	"$migration_password" == *'$'* ]] || fail '特殊字符密码测试样本构造失败'
migration_password_sql="$(printf '%s' "$migration_password" | LC_ALL=C sed "s/'/''/g")"
blocked_migration_password="blocked-${suffix}-migration"
blocked_runtime_password="blocked-${suffix}-runtime"
cross_password_a="Aa1!cross-a-${suffix}"
cross_password_b="Aa1!cross-b-${suffix}"

original_general_log=""
original_log_output=""
log_settings_saved=false
table_log_active=false
test_lock_directory=""
test_lock_pid=""
test_lock_name=""
test_lock_connection_id=""
active_session_directory=""
active_session_pid=""
active_session_connection_id=""
cross_test_directory=""
cross_job_a_pid=""
cross_job_b_pid=""

wait_for_first_line() {
	local directory="$1"
	local process_id="$2"
	local description="$3"
	local first_line
	for _ in {1..200}; do
		if [[ -s "$directory/output" ]]; then
			first_line="$(head -n 1 "$directory/output")"
			printf '%s\n' "$first_line"
			return 0
		fi
		if ! kill -0 "$process_id" >/dev/null 2>&1; then
			fail "${description}提前退出：$(tr '\n' ' ' < "$directory/error" | cut -c1-512)"
		fi
		sleep 0.05
	done
	fail "等待${description}就绪超时"
}

stop_test_lock_holder() {
	local session_exists
	if [[ -n "$test_lock_connection_id" ]]; then
		session_exists="$(mysql_root "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID = ${test_lock_connection_id}")" || session_exists=0
		if [[ "$session_exists" == 1 ]]; then
			mysql_root "KILL CONNECTION ${test_lock_connection_id}" >/dev/null 2>&1 || true
		fi
	fi
	if [[ -n "$test_lock_pid" ]]; then
		kill "$test_lock_pid" >/dev/null 2>&1 || true
		wait "$test_lock_pid" >/dev/null 2>&1 || true
	fi
	if [[ -n "$test_lock_directory" ]]; then
		rm -f -- "$test_lock_directory/output" "$test_lock_directory/error"
		rmdir -- "$test_lock_directory" >/dev/null 2>&1 || true
	fi
	test_lock_directory=""
	test_lock_pid=""
	test_lock_name=""
	test_lock_connection_id=""
}

start_test_lock_holder() {
	local username="$1"
	local first_line acquired unexpected_fields
	test_lock_name="$(mysql_root "SELECT CONCAT('ares_migration_account_', LEFT(SHA2('${username}', 256), 32))")" || \
		fail '无法计算账号级互斥锁测试名称'
	[[ "$test_lock_name" =~ ^ares_migration_account_[0-9a-f]{32}$ ]] || \
		fail '账号级互斥锁测试名称格式错误'
	[[ "${#test_lock_name}" -eq 55 ]] || fail '账号级互斥锁测试名称长度不是 55 字节'
	test_lock_directory="$(mktemp -d "${TMPDIR:-/tmp}/ares-account-lock-test.XXXXXX")" || \
		fail '无法创建账号锁测试状态目录'
	"$docker_bin" exec \
		--env "MYSQL_PWD=${root_password}" \
		"$test_container" \
		mysql --protocol=tcp --host=127.0.0.1 --user=root --connect-timeout=5 \
		--batch --skip-column-names --raw --unbuffered \
		--execute="SELECT CONNECTION_ID(), GET_LOCK('${test_lock_name}', 0); SELECT SLEEP(300);" \
		>"$test_lock_directory/output" 2>"$test_lock_directory/error" &
	test_lock_pid=$!
	first_line="$(wait_for_first_line "$test_lock_directory" "$test_lock_pid" '账号锁测试连接')"
	IFS=$'\t' read -r test_lock_connection_id acquired unexpected_fields <<< "$first_line"
	[[ "$test_lock_connection_id" =~ ^[0-9]+$ && "$acquired" == 1 && -z "$unexpected_fields" ]] || \
		fail "账号锁测试连接未获取锁：${first_line}"
	assert_query_equals "$test_lock_connection_id" \
		"SELECT IS_USED_LOCK('${test_lock_name}')" \
		'账号锁测试连接所有权不匹配'
}

stop_active_session() {
	local session_exists
	if [[ -n "$active_session_connection_id" ]]; then
		session_exists="$(mysql_root "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID = ${active_session_connection_id}")" || session_exists=0
		if [[ "$session_exists" == 1 ]]; then
			mysql_root "KILL CONNECTION ${active_session_connection_id}" >/dev/null 2>&1 || true
		fi
	fi
	if [[ -n "$active_session_pid" ]]; then
		kill "$active_session_pid" >/dev/null 2>&1 || true
		wait "$active_session_pid" >/dev/null 2>&1 || true
	fi
	if [[ -n "$active_session_directory" ]]; then
		rm -f -- "$active_session_directory/output" "$active_session_directory/error"
		rmdir -- "$active_session_directory" >/dev/null 2>&1 || true
	fi
	active_session_directory=""
	active_session_pid=""
	active_session_connection_id=""
}

start_active_session() {
	local username="$1"
	local password="$2"
	local selected_database="$3"
	local first_line
	active_session_directory="$(mktemp -d "${TMPDIR:-/tmp}/ares-account-session-test.XXXXXX")" || \
		fail '无法创建活跃会话测试状态目录'
	"$docker_bin" exec \
		--env "MYSQL_PWD=${password}" \
		"$test_container" \
		mysql --protocol=tcp --host=127.0.0.1 --user="$username" --database="$selected_database" --connect-timeout=5 \
		--batch --skip-column-names --raw --unbuffered \
		--execute='SELECT CONNECTION_ID(); SELECT SLEEP(300);' \
		>"$active_session_directory/output" 2>"$active_session_directory/error" &
	active_session_pid=$!
	first_line="$(wait_for_first_line "$active_session_directory" "$active_session_pid" '活跃迁移会话')"
	active_session_connection_id="$first_line"
	[[ "$active_session_connection_id" =~ ^[0-9]+$ ]] || \
		fail "活跃迁移会话编号不可识别：${active_session_connection_id}"
}

restore_general_log() {
	if [[ "$log_settings_saved" != true ]]; then
		return 0
	fi
	mysql_root "SET GLOBAL general_log = OFF;
		SET GLOBAL log_output = '${original_log_output}';
		SET GLOBAL general_log = ${original_general_log};" >/dev/null
	table_log_active=false
}

cleanup() {
	local status="$1"
	set +e
	mysql_socket_root "DROP USER IF EXISTS 'root'@'127.0.0.1'" >/dev/null 2>&1
	mysql_socket_root "REVOKE PROXY ON '${special_proxy_user_sql}'@'%' FROM 'root'@'%'" \
		>/dev/null 2>&1 || true
	if [[ -n "$cross_job_a_pid" ]]; then
		kill "$cross_job_a_pid" >/dev/null 2>&1 || true
		wait "$cross_job_a_pid" >/dev/null 2>&1 || true
	fi
	if [[ -n "$cross_job_b_pid" ]]; then
		kill "$cross_job_b_pid" >/dev/null 2>&1 || true
		wait "$cross_job_b_pid" >/dev/null 2>&1 || true
	fi
	if [[ -n "$cross_test_directory" ]]; then
		rm -f -- "$cross_test_directory/job-a" "$cross_test_directory/job-b"
		rmdir -- "$cross_test_directory" >/dev/null 2>&1 || true
	fi
	stop_active_session
	stop_test_lock_holder
	restore_general_log >/dev/null 2>&1
	mysql_root "DROP USER IF EXISTS
		'${migration_user}'@'%',
		'${runtime_user}'@'%',
		'${legacy_user}'@'%',
		'${blocked_migration_user}'@'%',
		'${blocked_runtime_user}'@'%',
		'${lock_migration_user}'@'%',
		'${lock_runtime_user}'@'%',
		'${unsafe_runtime_user}'@'%',
		'${legacy_pattern_user}'@'%',
		'${cross_user_a}'@'%',
		'${cross_user_b}'@'%',
		'${capability_migration_user}'@'%',
		'${capability_runtime_user}'@'%',
		'${case_migration_user}'@'%',
		'${case_variant_user}'@'%',
		'${case_runtime_user}'@'%',
		'${special_proxy_user_sql}'@'%',
		'${failure_role_prefix}''x'@'%';
		DROP DATABASE IF EXISTS \`${database}\`;
		DROP DATABASE IF EXISTS \`${decoy_database}\`;
		DROP DATABASE IF EXISTS \`${external_database}\`;
		DROP DATABASE IF EXISTS \`${cross_database}\`;
		DROP DATABASE IF EXISTS \`${capability_database}\`;
		DROP DATABASE IF EXISTS \`${case_database}\`;
		DROP DATABASE IF EXISTS \`${blocked_database}\`;
		DROP DATABASE IF EXISTS \`${lock_database}\`;" >/dev/null 2>&1
	trap - EXIT
	exit "$status"
}
trap 'cleanup $?' EXIT

server_identity="$(mysql_root 'SELECT VERSION(), @@version_comment')" || \
	fail '无法读取 MySQL 服务端版本'
server_version="${server_identity%%$'\t'*}"
server_comment="${server_identity#*$'\t'}"
[[ "$server_version" =~ ^8\.4\.[0-9]+([.-].*)?$ ]] || \
	fail "动态测试仅支持 MySQL 8.4.x，实际为 ${server_version}"
server_comment_lower="$(printf '%s' "$server_comment" | tr '[:upper:]' '[:lower:]')"
[[ "$server_comment_lower" != *mariadb* ]] || fail '动态测试不支持 MariaDB'

log_settings="$(mysql_root 'SELECT @@GLOBAL.general_log, @@GLOBAL.log_output')" || \
	fail '无法读取 general_log 配置'
IFS=$'\t' read -r original_general_log original_log_output unexpected_log_fields <<< "$log_settings"
[[ "$original_general_log" =~ ^[01]$ ]] || fail 'general_log 原始状态不可识别'
[[ "$original_log_output" =~ ^(FILE|TABLE|NONE|FILE,TABLE|TABLE,FILE)$ ]] || \
	fail "log_output 原始状态不可识别：${original_log_output}"
[[ -z "$unexpected_log_fields" ]] || fail 'general_log 配置返回了多余字段'
log_settings_saved=true

start_table_log() {
	mysql_root "SET GLOBAL general_log = OFF;
		SET GLOBAL log_output = 'TABLE';
		SET GLOBAL general_log = ON;" >/dev/null
	table_log_active=true
}

stop_table_log() {
	if [[ "$table_log_active" == true ]]; then
		mysql_root 'SET GLOBAL general_log = OFF' >/dev/null
		table_log_active=false
	fi
}

assert_query_equals 0 'SELECT @@GLOBAL.partial_revokes' \
	'动态授权隔离测试要求 MySQL 8.4 默认 partial_revokes=OFF'

printf '验证账号级互斥锁竞争在任何数据库写入前超时失败\n'
start_test_lock_holder "$lock_migration_user"
if lock_blocked_output="$(run_account_init \
	migrator \
	"$lock_database" \
	"$lock_migration_user" \
	"lock-${suffix}-migration" \
	"$lock_runtime_user" \
	"lock-${suffix}-runtime" \
	1 2>&1)"; then
	fail '账号级互斥锁已被占用时初始化脚本仍然成功'
fi
[[ "$lock_blocked_output" == *'等待账号级互斥锁超过 1 秒'* ]] || \
	fail '账号级互斥锁竞争失败原因不明确'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '${lock_database}'" \
	'账号锁竞争失败前不应创建数据库'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM mysql.user WHERE User IN ('${lock_migration_user}', '${lock_runtime_user}')" \
	'账号锁竞争失败前不应创建账号'
assert_query_equals "$test_lock_connection_id" \
	"SELECT IS_USED_LOCK('${test_lock_name}')" \
	'竞争失败不得释放或杀死其他任务持有的账号锁'
stop_test_lock_holder

printf '验证元数据可见性不足的管理身份在首写前被能力门禁拒绝\n'
mysql_socket_root "CREATE USER 'root'@'127.0.0.1' IDENTIFIED BY '${restricted_root_password}';
	GRANT PROCESS, SUPER, CREATE USER ON *.* TO 'root'@'127.0.0.1';
	GRANT SELECT ON mysql.user TO 'root'@'127.0.0.1';
	GRANT SELECT ON mysql.global_grants TO 'root'@'127.0.0.1';" >/dev/null
restricted_admin_succeeded=false
if restricted_admin_output="$(run_account_init \
	migrator \
	"$capability_database" \
	"$capability_migration_user" \
	"Aa1!cap-m-${suffix}" \
	"$capability_runtime_user" \
	"Aa1!cap-r-${suffix}" \
	10 \
	"$restricted_root_password" 2>&1)"; then
	restricted_admin_succeeded=true
fi
mysql_socket_root "DROP USER 'root'@'127.0.0.1'" >/dev/null
[[ "$restricted_admin_succeeded" == false ]] || \
	fail '缺少 session visibility 权限的管理身份仍完成了账号初始化'
[[ "$restricted_admin_output" == *'缺少直接全局 SELECT、TRIGGER、EVENT、SHOW VIEW 权限'* ]] || \
	fail "受限管理身份能力门禁失败原因不明确：${restricted_admin_output}"
assert_query_equals 0 \
	"SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '${capability_database}'" \
	'管理身份能力不足时不应创建数据库'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM mysql.user WHERE User IN ('${capability_migration_user}', '${capability_runtime_user}')" \
	'管理身份能力不足时不应创建目标账号'

printf '验证未知旧 schema grantee 在账号初始化写入前被拒绝\n'
mysql_root "CREATE DATABASE \`${blocked_database}\`;
	CREATE TABLE \`${blocked_database}\`.sentinel (id BIGINT PRIMARY KEY, payload VARCHAR(32) NOT NULL);
	INSERT INTO \`${blocked_database}\`.sentinel (id, payload) VALUES (1, 'untouched');
	CREATE USER '${legacy_user}'@'%' IDENTIFIED BY 'legacy-only-for-test' ACCOUNT LOCK;
	GRANT SELECT ON \`${blocked_grant_pattern}\`.* TO '${legacy_user}'@'%';" >/dev/null
blocked_log_started_at="$(mysql_root "SELECT DATE_FORMAT(NOW(6), '%Y-%m-%d %H:%i:%s.%f')")" || \
	fail '无法记录 fail-closed 审计起点'
start_table_log
if blocked_output="$(run_account_init \
	migrator \
	"$blocked_database" \
	"$blocked_migration_user" \
	"$blocked_migration_password" \
	"$blocked_runtime_user" \
	"$blocked_runtime_password" 2>&1)"; then
	fail '存在未知旧 schema grantee 时账号初始化仍然成功'
fi
stop_table_log
[[ "$blocked_output" == *'非 Ares 身份授权'* ]] || \
	fail '未知旧 schema grantee 的失败原因不明确'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM mysql.user WHERE User IN ('${blocked_migration_user}', '${blocked_runtime_user}')" \
	'fail-closed 后不应创建 Ares 账号'
assert_query_equals 'untouched' \
	"SELECT payload FROM \`${blocked_database}\`.sentinel WHERE id = 1" \
	'fail-closed 后业务数据不应变化'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM mysql.general_log
	WHERE event_time >= '${blocked_log_started_at}'
		AND command_type = 'Query'
		AND UPPER(LTRIM(CONVERT(argument USING utf8mb4))) NOT LIKE 'SELECT%'
		AND UPPER(LTRIM(CONVERT(argument USING utf8mb4))) NOT LIKE 'KILL CONNECTION%'
		AND UPPER(LTRIM(CONVERT(argument USING utf8mb4))) NOT LIKE 'SET SESSION %'
		AND UPPER(LTRIM(CONVERT(argument USING utf8mb4))) NOT LIKE 'SET GLOBAL GENERAL_LOG%'" \
	'检测到 fail-closed 边界之后的持久化写语句'
mysql_root "DROP USER '${legacy_user}'@'%'; DROP DATABASE \`${blocked_database}\`;" >/dev/null

printf '验证 fresh migrator、锁定状态、密码审计日志与幂等收敛\n'
mysql_root "CREATE DATABASE \`${decoy_database}\`;
	CREATE TABLE \`${decoy_database}\`.secret_probe (id BIGINT PRIMARY KEY, payload VARCHAR(32));
	INSERT INTO \`${decoy_database}\`.secret_probe VALUES (1, 'decoy-secret');" >/dev/null
start_table_log
run_account_init \
	migrator \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" >/dev/null
assert_query_equals $'Y\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${migration_user}')
	FROM mysql.user u WHERE u.User = '${migration_user}' AND u.Host = '%'" \
	'fresh migrator 必须保持锁定且无会话'
if mysql_as_user "$migration_user" "$migration_password" "$database" 'SELECT 1' >/dev/null 2>&1; then
	fail '锁定的 migrator 仍可使用长期凭据登录'
fi
expected_grant_pattern="${database//_/\\_}"
expected_grant_pattern_hex="$(printf '%s' "$expected_grant_pattern" | hex_upper)"
assert_query_equals "$expected_grant_pattern_hex" \
	"SELECT HEX(Db) FROM mysql.db WHERE BINARY User = BINARY '${migration_user}' AND Host = '%'" \
	'migrator 数据库授权未转义 partial_revokes=OFF 的通配符'
mysql_root "SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES';
	ALTER USER '${migration_user}'@'%' IDENTIFIED BY '${migration_password_sql}' ACCOUNT UNLOCK" >/dev/null
mysql_as_user "$migration_user" "$migration_password" "$database" 'SELECT 1' >/dev/null || \
	fail 'migrator 安全转义授权未覆盖目标数据库'
if mysql_as_user "$migration_user" "$migration_password" "$decoy_database" \
	'SELECT payload FROM secret_probe' >/dev/null 2>&1; then
	fail 'migrator 的数据库 pattern 授权越界读取了诱饵数据库'
fi
mysql_root "ALTER USER '${migration_user}'@'%' ACCOUNT LOCK" >/dev/null

printf '验证账号会话枚举区分大小写且不会误杀 case-variant 身份\n'
run_account_init migrator "$case_database" \
	"$case_migration_user" "$case_password" "$case_runtime_user" "$case_password" >/dev/null
mysql_root "ALTER USER '${case_migration_user}'@'%' ACCOUNT UNLOCK;
	CREATE USER '${case_variant_user}'@'%' IDENTIFIED BY '${case_password}';
	GRANT SELECT ON \`${decoy_database}\`.* TO '${case_variant_user}'@'%';" >/dev/null
start_active_session "$case_variant_user" "$case_password" "$decoy_database"
run_account_init migrator "$case_database" \
	"$case_migration_user" "$case_password" "$case_runtime_user" "$case_password" >/dev/null
assert_query_equals 1 \
	"SELECT COUNT(*) FROM information_schema.PROCESSLIST
	WHERE ID = ${active_session_connection_id} AND BINARY USER = BINARY '${case_variant_user}'" \
	'目标账号收敛误杀了仅大小写不同的第三方会话'
assert_query_equals $'Y\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p
			WHERE BINARY p.USER = BINARY '${case_migration_user}')
	FROM mysql.user u WHERE BINARY u.User = BINARY '${case_migration_user}' AND u.Host = '%'" \
	'大小写敏感会话收敛后目标 migrator 状态不正确'
stop_active_session

printf '验证 runtime 目标账号锁竞争会在首写前失败并释放已获取的同组锁\n'
start_test_lock_holder "$runtime_user"
if runtime_lock_blocked_output="$(run_account_init \
	runtime \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" \
	1 2>&1)"; then
	fail 'runtime 目标账号锁被占用时初始化脚本仍然成功'
fi
[[ "$runtime_lock_blocked_output" == *'等待账号级互斥锁超过 1 秒'* ]] || \
	fail "runtime 目标账号锁竞争失败原因不明确：${runtime_lock_blocked_output}"
assert_query_equals 0 \
	"SELECT COUNT(*) FROM mysql.user WHERE BINARY User = BINARY '${runtime_user}' AND Host = '%'" \
	'runtime 目标账号锁竞争失败前不应创建账号'
migration_lock_name="$(mysql_root \
	"SELECT CONCAT('ares_migration_account_', LEFT(SHA2('${migration_user}', 256), 32))")"
assert_query_equals 1 "SELECT IS_FREE_LOCK('${migration_lock_name}')" \
	'runtime 双锁竞争失败后不得残留迁移账号锁'
assert_query_equals "$test_lock_connection_id" "SELECT IS_USED_LOCK('${test_lock_name}')" \
	'runtime 双锁竞争失败不得释放其他任务持有的目标账号锁'
stop_test_lock_holder

printf '验证并发账号任务不会终止受同名锁保护的 guarded 迁移会话\n'
start_test_lock_holder "$migration_user"
mysql_root "SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES';
	ALTER USER '${migration_user}'@'%' IDENTIFIED BY '${migration_password_sql}' ACCOUNT UNLOCK" >/dev/null
start_active_session "$migration_user" "$migration_password" "$database"
mysql_root "ALTER USER '${migration_user}'@'%' ACCOUNT LOCK" >/dev/null
if guarded_blocked_output="$(run_account_init \
	migrator \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" \
	1 2>&1)"; then
	fail 'guarded 迁移持有账号锁时初始化脚本仍然成功'
fi
[[ "$guarded_blocked_output" == *'等待账号级互斥锁超过 1 秒'* ]] || \
	fail 'guarded 迁移并发竞争失败原因不明确'
assert_query_equals "$test_lock_connection_id" \
	"SELECT IS_USED_LOCK('${test_lock_name}')" \
	'并发账号任务不得释放 guarded 迁移持有的账号锁'
assert_query_equals 1 \
	"SELECT COUNT(*) FROM information_schema.PROCESSLIST
	WHERE ID = ${active_session_connection_id} AND BINARY USER = BINARY '${migration_user}'" \
	'并发账号任务不得终止 guarded 迁移活跃会话'
assert_query_equals Y \
	"SELECT account_locked FROM mysql.user WHERE BINARY User = BINARY '${migration_user}' AND Host = '%'" \
	'并发账号任务不得解锁 guarded 迁移账号'
if mysql_as_user "$migration_user" "$migration_password" "$database" 'SELECT 1' >/dev/null 2>&1; then
	fail '并发竞争期间锁定的 migrator 仍能建立新连接'
fi
if guarded_runtime_blocked_output="$(run_account_init \
	runtime \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" \
	1 2>&1)"; then
	fail 'guarded 迁移持有账号锁时 runtime 账号任务仍然成功'
fi
[[ "$guarded_runtime_blocked_output" == *'等待账号级互斥锁超过 1 秒'* ]] || \
	fail 'runtime 账号任务未使用共享账号锁'
assert_query_equals 1 \
	"SELECT COUNT(*) FROM information_schema.PROCESSLIST
	WHERE ID = ${active_session_connection_id} AND BINARY USER = BINARY '${migration_user}'" \
	'runtime 账号任务不得终止 guarded 迁移活跃会话'
stop_test_lock_holder

printf '验证脚本拿到账号锁后仍拒绝 locked+active guarded 稳定态\n'
if guarded_orphan_output="$(run_account_init \
	migrator \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" 2>&1)"; then
	fail '迁移账号 locked+active 时初始化脚本仍然成功'
fi
[[ "$guarded_orphan_output" == *'已锁定迁移账号仍有活跃会话'* ]] || \
	fail 'locked+active guarded 稳定态失败原因不明确'
assert_query_equals 1 \
	"SELECT COUNT(*) FROM information_schema.PROCESSLIST
	WHERE ID = ${active_session_connection_id} AND BINARY USER = BINARY '${migration_user}'" \
	'脚本拿到账号锁后不得终止 locked+active guarded 会话'
assert_query_equals Y \
	"SELECT account_locked FROM mysql.user WHERE BINARY User = BINARY '${migration_user}' AND Host = '%'" \
	'脚本拿到账号锁后不得改变 locked+active guarded 锁态'
stop_active_session
mysql_root "ALTER USER '${migration_user}'@'%' ACCOUNT UNLOCK" >/dev/null
if ! mysql_as_user "$migration_user" "$migration_password" "$database" 'SELECT 1' >/dev/null 2>&1; then
	fail 'locked+active guarded 拒绝路径不应轮换迁移账号密码'
fi
mysql_root "ALTER USER '${migration_user}'@'%' ACCOUNT LOCK" >/dev/null
assert_query_equals $'Y\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${migration_user}')
	FROM mysql.user u WHERE u.User = '${migration_user}' AND u.Host = '%'" \
	'guarded 迁移结束后的模拟状态未正确收敛'

printf '验证含单引号的角色与 PROXY 身份可安全清理\n'
mysql_socket_root "CREATE ROLE '${failure_role_prefix}''x'@'%';
	CREATE USER '${special_proxy_user_sql}'@'%' IDENTIFIED BY 'Aa1!proxy-${suffix}' ACCOUNT LOCK;
	GRANT '${failure_role_prefix}''x'@'%' TO '${migration_user}'@'%';
	GRANT PROXY ON '${special_proxy_user_sql}'@'%' TO 'root'@'%' WITH GRANT OPTION;
	GRANT PROXY ON '${special_proxy_user_sql}'@'%' TO '${migration_user}'@'%';
	SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES';
	ALTER USER '${migration_user}'@'%' IDENTIFIED BY '${migration_password_sql}' ACCOUNT UNLOCK;" >/dev/null
start_active_session "$migration_user" "$migration_password" "$database"
run_account_init \
	migrator \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" >/dev/null
assert_query_equals $'Y\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${migration_user}')
	FROM mysql.user u WHERE u.User = '${migration_user}' AND u.Host = '%'" \
	'特殊角色/PROXY 清理后 migrator 未锁定或仍有旧会话'
assert_query_equals $'0\t0' \
	"SELECT
		(SELECT COUNT(*) FROM mysql.role_edges WHERE TO_USER = '${migration_user}' AND TO_HOST = '%'),
		(SELECT COUNT(*) FROM mysql.proxies_priv WHERE User = '${migration_user}' AND Host = '%')" \
	'含单引号的角色或 PROXY 授权未被安全清理'
stop_active_session
mysql_socket_root "REVOKE PROXY ON '${special_proxy_user_sql}'@'%' FROM 'root'@'%';
	DROP ROLE '${failure_role_prefix}''x'@'%';
	DROP USER '${special_proxy_user_sql}'@'%';" >/dev/null

mysql_root "GRANT DROP ON \`${expected_grant_pattern}\`.* TO '${migration_user}'@'%';" >/dev/null
run_account_init \
	migrator \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" >/dev/null
assert_query_equals $'Y\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${migration_user}')
	FROM mysql.user u WHERE u.User = '${migration_user}' AND u.Host = '%'" \
	'重复初始化后 migrator 必须保持锁定且无会话'
assert_query_equals 'ALTER,CREATE,DELETE,INDEX,INSERT,REFERENCES,SELECT,UPDATE' \
	"SELECT COALESCE(GROUP_CONCAT(PRIVILEGE_TYPE ORDER BY PRIVILEGE_TYPE SEPARATOR ','), '')
	FROM information_schema.SCHEMA_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${migration_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
		AND HEX(TABLE_SCHEMA) = '${expected_grant_pattern_hex}'" \
	'migrator 权限必须精确收敛且不含 DROP'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${migration_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
		AND PRIVILEGE_TYPE <> 'USAGE'" \
	'migrator 不应持有全局权限'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM information_schema.TABLE_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${migration_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))" \
	'migrator 不应持有额外表级权限'

printf '验证目标账号的全局、跨库及外部对象授权在首写前 fail-closed\n'
mysql_root "CREATE DATABASE \`${external_database}\`;
	CREATE TABLE \`${external_database}\`.external_probe (id BIGINT PRIMARY KEY, payload VARCHAR(32));
	CREATE PROCEDURE \`${external_database}\`.external_proc() SELECT 1;
	CREATE USER '${unsafe_runtime_user}'@'%' IDENTIFIED BY 'Aa1!unsafe-${suffix}' ACCOUNT LOCK;
	GRANT PROCESS ON *.* TO '${unsafe_runtime_user}'@'%';
	GRANT SELECT ON \`${external_database}\`.* TO '${unsafe_runtime_user}'@'%';
	GRANT INSERT ON \`${external_database}\`.external_probe TO '${unsafe_runtime_user}'@'%';
	GRANT SELECT (payload) ON \`${external_database}\`.external_probe TO '${unsafe_runtime_user}'@'%';
	GRANT EXECUTE ON PROCEDURE \`${external_database}\`.external_proc TO '${unsafe_runtime_user}'@'%';" >/dev/null
unsafe_account_before="$(mysql_root \
	"SELECT account_locked, HEX(authentication_string) FROM mysql.user WHERE BINARY User = BINARY '${unsafe_runtime_user}' AND Host = '%'")"
assert_query_equals $'1\t0\t1\t1\t1\t1' \
	"SELECT
		(SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES
			WHERE GRANTEE = CONCAT(CHAR(39), '${unsafe_runtime_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
				AND PRIVILEGE_TYPE <> 'USAGE'),
		(SELECT COUNT(*) FROM mysql.global_grants WHERE BINARY USER = BINARY '${unsafe_runtime_user}' AND HOST = '%'),
		(SELECT COUNT(*) FROM mysql.db WHERE BINARY User = BINARY '${unsafe_runtime_user}' AND Host = '%'),
		(SELECT COUNT(*) FROM mysql.tables_priv WHERE BINARY User = BINARY '${unsafe_runtime_user}' AND Host = '%'),
		(SELECT COUNT(*) FROM mysql.columns_priv WHERE BINARY User = BINARY '${unsafe_runtime_user}' AND Host = '%'),
		(SELECT COUNT(*) FROM mysql.procs_priv WHERE BINARY User = BINARY '${unsafe_runtime_user}' AND Host = '%')" \
	'跨部署危险授权测试数据不完整'
if unsafe_output="$(run_account_init \
	runtime \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$unsafe_runtime_user" \
	"Aa1!unsafe-${suffix}" 2>&1)"; then
	fail '持有全局或外部对象授权的 runtime 账号仍被初始化'
fi
[[ "$unsafe_output" == *'拒绝跨部署复用账号'* ]] || \
	fail '跨部署危险授权失败原因不明确'
assert_query_equals "$unsafe_account_before" \
	"SELECT account_locked, HEX(authentication_string) FROM mysql.user WHERE BINARY User = BINARY '${unsafe_runtime_user}' AND Host = '%'" \
	'危险授权拒绝路径不得改变目标账号锁态或凭据'

printf '验证旧版未转义当前 schema pattern 在首写前 fail-closed\n'
mysql_root "CREATE USER '${legacy_pattern_user}'@'%' IDENTIFIED BY 'Aa1!legacy-pattern-${suffix}' ACCOUNT LOCK;
	GRANT SELECT ON \`${database}\`.* TO '${legacy_pattern_user}'@'%';" >/dev/null
legacy_pattern_before="$(mysql_root \
	"SELECT account_locked, HEX(authentication_string), HEX(Db)
	FROM mysql.user JOIN mysql.db ON mysql.user.User = mysql.db.User AND mysql.user.Host = mysql.db.Host
	WHERE BINARY mysql.user.User = BINARY '${legacy_pattern_user}' AND mysql.user.Host = '%'")"
if legacy_pattern_output="$(run_account_init \
	runtime \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$legacy_pattern_user" \
	"Aa1!legacy-pattern-${suffix}" 2>&1)"; then
	fail '旧版未转义 schema pattern 仍被静默接纳'
fi
[[ "$legacy_pattern_output" == *'拒绝跨部署复用账号'* ]] || \
	fail '旧版未转义 schema pattern 失败原因不明确'
assert_query_equals "$legacy_pattern_before" \
	"SELECT account_locked, HEX(authentication_string), HEX(Db)
	FROM mysql.user JOIN mysql.db ON mysql.user.User = mysql.db.User AND mysql.user.Host = mysql.db.Host
	WHERE BINARY mysql.user.User = BINARY '${legacy_pattern_user}' AND mysql.user.Host = '%'" \
	'旧版危险 pattern 拒绝路径不得修改账号或授权'
mysql_root "DROP USER '${legacy_pattern_user}'@'%'" >/dev/null

printf '验证授权中途失败不会留下 runtime 解锁、会话或原始凭据\n'
if runtime_failure_output="$(run_account_init \
	runtime \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" 2>&1)"; then
	fail '业务表尚不存在时 runtime 授权应当失败'
fi
[[ "$runtime_failure_output" == *'fail-closed 收敛'* ]] || \
	fail 'runtime 授权中途失败没有触发 fail-closed 收敛'
assert_query_equals $'Y\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${runtime_user}')
	FROM mysql.user u WHERE u.User = '${runtime_user}' AND u.Host = '%'" \
	'runtime 授权失败后账号未锁定或仍有会话'
mysql_root "ALTER USER '${runtime_user}'@'%' ACCOUNT UNLOCK" >/dev/null
if mysql_as_user "$runtime_user" "$runtime_password" "$database" 'SELECT 1' >/dev/null 2>&1; then
	fail 'runtime 授权失败后原始密码仍然有效'
fi
mysql_root "ALTER USER '${runtime_user}'@'%' ACCOUNT LOCK" >/dev/null

tables=(
	apps
	app_configs
	app_config_domains
	task_record
	task_record_images
	pipelines
	pipelines_job_combination
	env_configs
	integration_settings
	dev_language_rules
	release_workflows
	release_workflow_versions
	app_config_workflows
	task_step_records
	schema_migrations
	runtime_read_only
)
create_tables_sql=''
for table_name in "${tables[@]}"; do
	create_tables_sql+="CREATE TABLE \`${database}\`.\`${table_name}\` (id BIGINT PRIMARY KEY, payload VARCHAR(64) NULL);"
done
mysql_root "$create_tables_sql" >/dev/null

run_account_init \
	runtime \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" >/dev/null
assert_query_equals $'N\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${runtime_user}')
	FROM mysql.user u WHERE u.User = '${runtime_user}' AND u.Host = '%'" \
	'runtime 初始化后必须解锁且无残留验证会话'
assert_query_equals "$expected_grant_pattern_hex" \
	"SELECT HEX(Db) FROM mysql.db WHERE BINARY User = BINARY '${runtime_user}' AND Host = '%'" \
	'runtime 数据库授权未转义 partial_revokes=OFF 的通配符'
if mysql_as_user "$runtime_user" "$runtime_password" "$decoy_database" \
	'SELECT payload FROM secret_probe' >/dev/null 2>&1; then
	fail 'runtime 的数据库 pattern 授权越界读取了诱饵数据库'
fi

mysql_as_user "$runtime_user" "$runtime_password" "$database" \
	"INSERT INTO apps (id, payload) VALUES (1, 'inserted');
	UPDATE apps SET payload = 'updated' WHERE id = 1;
	DELETE FROM apps WHERE id = 1;
	SELECT COUNT(*) FROM schema_migrations;" >/dev/null || \
	fail 'runtime 应能执行允许的 DML 与只读 ledger 查询'
if mysql_as_user "$runtime_user" "$runtime_password" "$database" \
	'CREATE TABLE runtime_forbidden (id BIGINT PRIMARY KEY)' >/dev/null 2>&1; then
	fail 'runtime 不应拥有 DDL 权限'
fi
if mysql_as_user "$runtime_user" "$runtime_password" "$database" \
	"INSERT INTO schema_migrations (id, payload) VALUES (1, 'forbidden')" >/dev/null 2>&1; then
	fail 'runtime 不应拥有 schema_migrations 写权限'
fi
if mysql_as_user "$runtime_user" "$runtime_password" "$database" \
	"INSERT INTO runtime_read_only (id, payload) VALUES (1, 'forbidden')" >/dev/null 2>&1; then
	fail 'runtime 不应对未列入白名单的表写入'
fi

assert_query_equals SELECT \
	"SELECT COALESCE(GROUP_CONCAT(PRIVILEGE_TYPE ORDER BY PRIVILEGE_TYPE SEPARATOR ','), '')
	FROM information_schema.SCHEMA_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${runtime_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
		AND HEX(TABLE_SCHEMA) = '${expected_grant_pattern_hex}'" \
	'runtime schema 级权限必须仅含 SELECT'
assert_query_equals 0 \
	"SELECT COUNT(*)
	FROM information_schema.TABLE_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${runtime_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
		AND TABLE_SCHEMA = '${database}'
		AND TABLE_NAME NOT IN (
			'apps', 'app_configs', 'app_config_domains', 'task_record', 'task_record_images',
			'pipelines', 'pipelines_job_combination', 'env_configs', 'integration_settings',
			'dev_language_rules', 'release_workflows', 'release_workflow_versions',
			'app_config_workflows', 'task_step_records'
		)" \
	'runtime DML 表白名单不匹配'
assert_query_equals $'14\t42\tDELETE,INSERT,UPDATE' \
	"SELECT COUNT(DISTINCT TABLE_NAME), COUNT(*),
		COALESCE(GROUP_CONCAT(DISTINCT PRIVILEGE_TYPE ORDER BY PRIVILEGE_TYPE SEPARATOR ','), '')
	FROM information_schema.TABLE_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${runtime_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
		AND TABLE_SCHEMA = '${database}'" \
	'runtime 表级权限必须仅为 14 张表的 DML'
assert_query_equals 0 \
	"SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${runtime_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
		AND PRIVILEGE_TYPE <> 'USAGE'" \
	'runtime 不应持有全局权限'

mysql_root "GRANT CREATE ON \`${expected_grant_pattern}\`.* TO '${runtime_user}'@'%';" >/dev/null
run_account_init \
	runtime \
	"$database" \
	"$migration_user" \
	"$migration_password" \
	"$runtime_user" \
	"$runtime_password" >/dev/null
assert_query_equals SELECT \
	"SELECT COALESCE(GROUP_CONCAT(PRIVILEGE_TYPE ORDER BY PRIVILEGE_TYPE SEPARATOR ','), '')
	FROM information_schema.SCHEMA_PRIVILEGES
	WHERE GRANTEE = CONCAT(CHAR(39), '${runtime_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
		AND HEX(TABLE_SCHEMA) = '${expected_grant_pattern_hex}'" \
	'重复初始化必须移除 runtime 漂移的 CREATE 权限'
if mysql_as_user "$runtime_user" "$runtime_password" "$database" \
	'CREATE TABLE runtime_still_forbidden (id BIGINT PRIMARY KEY)' >/dev/null 2>&1; then
	fail '重复初始化后 runtime 仍拥有 DDL 权限'
fi
assert_query_equals $'Y\t0' \
	"SELECT u.account_locked,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${migration_user}')
	FROM mysql.user u WHERE u.User = '${migration_user}' AND u.Host = '%'" \
	'runtime 初始化不得解锁或遗留 migrator 会话'

stop_table_log
migration_marker_hex="$(printf '%s' "$migration_marker" | hex_upper)"
runtime_marker_hex="$(printf '%s' "$runtime_marker" | hex_upper)"
assert_query_equals 0 \
	"SELECT COUNT(*) FROM mysql.general_log
	WHERE LOCATE('${migration_marker}', CONVERT(argument USING utf8mb4)) > 0
		OR LOCATE('${runtime_marker}', CONVERT(argument USING utf8mb4)) > 0
		OR LOCATE('${migration_marker_hex}', UPPER(CONVERT(argument USING utf8mb4))) > 0
		OR LOCATE('${runtime_marker_hex}', UPPER(CONVERT(argument USING utf8mb4))) > 0" \
	'general_log 泄露了明文或可逆十六进制账号密码'
redacted_statement_count="$(mysql_root "SELECT COUNT(*) FROM mysql.general_log
	WHERE argument LIKE '%<secret>%'
		AND (argument LIKE '%${migration_user}%' OR argument LIKE '%${runtime_user}%')")" || \
	fail '无法验证 general_log 脱敏记录'
[[ "$redacted_statement_count" =~ ^[0-9]+$ && "$redacted_statement_count" -ge 4 ]] || \
	fail "general_log 中账号口令语句未按预期脱敏，记录数=${redacted_statement_count}"

printf '验证交叉账号配置并发遵循全局锁序且不残留 named lock\n'
run_account_init migrator "$cross_database" \
	"$cross_user_a" "$cross_password_a" "$cross_user_b" "$cross_password_b" >/dev/null
run_account_init migrator "$cross_database" \
	"$cross_user_b" "$cross_password_b" "$cross_user_a" "$cross_password_a" >/dev/null
cross_tables_sql=''
for table_name in "${tables[@]}"; do
	cross_tables_sql+="CREATE TABLE \`${cross_database}\`.\`${table_name}\` (id BIGINT PRIMARY KEY, payload VARCHAR(64) NULL);"
done
mysql_root "$cross_tables_sql" >/dev/null
cross_test_directory="$(mktemp -d "${TMPDIR:-/tmp}/ares-account-cross-lock.XXXXXX")" || \
	fail '无法创建交叉账号并发测试目录'
run_account_init runtime "$cross_database" \
	"$cross_user_a" "$cross_password_a" "$cross_user_b" "$cross_password_b" \
	>"$cross_test_directory/job-a" 2>&1 &
cross_job_a_pid=$!
run_account_init runtime "$cross_database" \
	"$cross_user_b" "$cross_password_b" "$cross_user_a" "$cross_password_a" \
	>"$cross_test_directory/job-b" 2>&1 &
cross_job_b_pid=$!
cross_deadline=$((SECONDS + 20))
while kill -0 "$cross_job_a_pid" >/dev/null 2>&1 || \
	kill -0 "$cross_job_b_pid" >/dev/null 2>&1; do
	((SECONDS < cross_deadline)) || fail '交叉账号并发初始化未在 20 秒内退出，疑似锁序死锁'
	sleep 0.1
done
if wait "$cross_job_a_pid"; then cross_status_a=0; else cross_status_a=$?; fi
if wait "$cross_job_b_pid"; then cross_status_b=0; else cross_status_b=$?; fi
cross_job_a_pid=""
cross_job_b_pid=""
if ! { [[ "$cross_status_a" == 0 && "$cross_status_b" != 0 ]] || \
	[[ "$cross_status_a" != 0 && "$cross_status_b" == 0 ]]; }; then
	fail "交叉账号并发应恰有一个成功（A=${cross_status_a}, B=${cross_status_b}）"
fi
cross_lock_a="$(mysql_root \
	"SELECT CONCAT('ares_migration_account_', LEFT(SHA2('${cross_user_a}', 256), 32))")"
cross_lock_b="$(mysql_root \
	"SELECT CONCAT('ares_migration_account_', LEFT(SHA2('${cross_user_b}', 256), 32))")"
assert_query_equals $'1\t1' \
	"SELECT IS_FREE_LOCK('${cross_lock_a}'), IS_FREE_LOCK('${cross_lock_b}')" \
	'交叉账号并发完成后存在残留账号锁'
rm -f -- "$cross_test_directory/job-a" "$cross_test_directory/job-b"
rmdir -- "$cross_test_directory"
cross_test_directory=""

printf 'Ares MySQL 账号动态集成测试通过（MySQL %s）\n' "$server_version"
