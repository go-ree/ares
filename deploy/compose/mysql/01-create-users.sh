#!/usr/bin/env bash

set -Eeuo pipefail

fail() {
    printf 'Ares MySQL 初始化失败：%s\n' "$1" >&2
    exit 1
}

require_value() {
    local name="$1"
    [[ -n "${!name:-}" ]] || fail "环境变量 ${name} 不能为空"
}

validate_identifier() {
    local name="$1"
    local value="$2"
    local maximum_length="$3"

    [[ "$value" =~ ^[A-Za-z0-9_]+$ ]] || fail "${name} 只能包含 ASCII 字母、数字和下划线"
    ((${#value} <= maximum_length)) || fail "${name} 长度不能超过 ${maximum_length}"
}

validate_timeout_seconds() {
    local name="$1"
    local value="$2"
    local maximum="$3"

    [[ "$value" =~ ^[1-9][0-9]*$ ]] || fail "${name} 必须是正整数秒数"
    ((10#$value <= maximum)) || fail "${name} 不能超过 ${maximum} 秒"
}

escape_sql_string_literal() {
	# The caller enables NO_BACKSLASH_ESCAPES. Doubling apostrophes is then the
	# only transformation needed to keep an arbitrary non-multiline password
	# inside a SQL string literal, without first logging it through SET/PREPARE.
	LC_ALL=C sed "s/'/''/g"
}

for required_name in \
    ARES_DATABASE_ACCOUNT_ROLE \
    MYSQL_HOST \
    MYSQL_DATABASE \
    MYSQL_ROOT_PASSWORD \
    MYSQL_RUNTIME_USER \
    MYSQL_MIGRATION_USER; do
    require_value "$required_name"
done

validate_identifier MYSQL_DATABASE "$MYSQL_DATABASE" 64
validate_identifier MYSQL_RUNTIME_USER "$MYSQL_RUNTIME_USER" 32
validate_identifier MYSQL_MIGRATION_USER "$MYSQL_MIGRATION_USER" 32

[[ "${MYSQL_RUNTIME_USER,,}" != root ]] || fail 'MYSQL_RUNTIME_USER 不能使用 root'
[[ "${MYSQL_MIGRATION_USER,,}" != root ]] || fail 'MYSQL_MIGRATION_USER 不能使用 root'
[[ "$MYSQL_RUNTIME_USER" != "$MYSQL_MIGRATION_USER" ]] || fail '运行时账号与迁移账号必须不同'

case "$ARES_DATABASE_ACCOUNT_ROLE" in
    migrator)
        require_value MYSQL_MIGRATION_PASSWORD
        account_user="$MYSQL_MIGRATION_USER"
        account_password="$MYSQL_MIGRATION_PASSWORD"
        ;;
    runtime)
        require_value MYSQL_RUNTIME_PASSWORD
        account_user="$MYSQL_RUNTIME_USER"
        account_password="$MYSQL_RUNTIME_PASSWORD"
        ;;
    *)
        fail 'ARES_DATABASE_ACCOUNT_ROLE 只能是 migrator 或 runtime'
        ;;
esac

mysql_connect_timeout_seconds="${ARES_DATABASE_ACCOUNT_CONNECT_TIMEOUT_SECONDS:-5}"
account_init_timeout_seconds="${ARES_DATABASE_ACCOUNT_INIT_TIMEOUT_SECONDS:-60}"
account_lock_timeout_seconds="${ARES_DATABASE_ACCOUNT_LOCK_TIMEOUT_SECONDS:-30}"
validate_timeout_seconds ARES_DATABASE_ACCOUNT_CONNECT_TIMEOUT_SECONDS \
    "$mysql_connect_timeout_seconds" 30
validate_timeout_seconds ARES_DATABASE_ACCOUNT_INIT_TIMEOUT_SECONDS \
    "$account_init_timeout_seconds" 300
validate_timeout_seconds ARES_DATABASE_ACCOUNT_LOCK_TIMEOUT_SECONDS \
    "$account_lock_timeout_seconds" 300
command -v timeout >/dev/null 2>&1 || fail '当前环境缺少 timeout 命令，无法保证账号初始化的总时限'

account_init_deadline=$((SECONDS + 10#$account_init_timeout_seconds))

[[ "$account_password" != *$'\n'* && "$account_password" != *$'\r'* ]] || \
	fail '数据库账号密码不能包含换行符'
account_password_sql="$(printf '%s' "$account_password" | escape_sql_string_literal)"

printf '为数据库 %s 初始化 Ares %s 账号 %s\n' \
    "$MYSQL_DATABASE" "$ARES_DATABASE_ACCOUNT_ROLE" "$account_user"

mysql_command=(
    mysql
    --protocol=tcp
    --host="$MYSQL_HOST"
    --user=root
    --connect-timeout="$mysql_connect_timeout_seconds"
)

account_lock_names=()
acquired_account_lock_names=()
account_lock_connection_id=""
account_lock_holder_pid=""
account_lock_directory=""
account_lock_acquired=false
root_session_ready=false
root_session_input_open=false
root_session_output_open=false
containment_armed=false
task_succeeded=false

emergency_mysql() {
    MYSQL_PWD="$MYSQL_ROOT_PASSWORD" timeout 10s "${mysql_command[@]}" \
        --skip-reconnect --binary-mode --skip-commands "$@"
}

abort_root_mysql_session() {
    if [[ -n "$account_lock_connection_id" ]]; then
        emergency_mysql --batch --skip-column-names --raw \
            --execute="KILL CONNECTION ${account_lock_connection_id}" \
            >/dev/null 2>&1 || true
    fi
    if [[ -n "$account_lock_holder_pid" ]]; then
        kill "$account_lock_holder_pid" >/dev/null 2>&1 || true
    fi
}

root_mysql_batch() {
    local statement="$1"
    local timeout_seconds="${2:-10}"
    local marker marker_hex error_before error_after line output=""
    local read_deadline remaining status

    [[ "$root_session_ready" == true && "$root_session_input_open" == true && \
        "$root_session_output_open" == true ]] || return 1
    kill -0 "$account_lock_holder_pid" >/dev/null 2>&1 || return 1
    [[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] || return 1

    marker_hex="$(LC_ALL=C od -An -N16 -tx1 /dev/urandom 2>/dev/null | tr -d ' \n')"
    [[ "$marker_hex" =~ ^[0-9a-f]{32}$ ]] || return 1
    marker="__ares_mysql_batch_${marker_hex}__"
    error_before="$(LC_ALL=C wc -c < "$account_lock_directory/error" | tr -d '[:space:]')"
    [[ "$error_before" =~ ^[0-9]+$ ]] || return 1

    # The extra delimiter safely terminates both --execute-style statements and
    # heredocs that already end in a semicolon. The marker makes one batch
    # synchronous without ever echoing SQL (and therefore passwords) to logs.
    if ! printf '%s\n;\nSELECT '\''%s'\'';\n' "$statement" "$marker" >&8; then
        abort_root_mysql_session
        return 1
    fi

    read_deadline=$((SECONDS + 10#$timeout_seconds))
    while true; do
        remaining=$((read_deadline - SECONDS))
        if ((remaining <= 0)); then
            abort_root_mysql_session
            return 124
        fi
        if IFS= read -r -t "$remaining" line <&9; then
            if [[ "$line" == "$marker" ]]; then
                break
            fi
            if [[ -n "$output" ]]; then
                output+=$'\n'
            fi
            output+="$line"
        else
            status=$?
            abort_root_mysql_session
            return "$status"
        fi
    done

    error_after="$(LC_ALL=C wc -c < "$account_lock_directory/error" | tr -d '[:space:]')"
    [[ "$error_after" =~ ^[0-9]+$ ]] || return 1
    if ((error_after != error_before)); then
        # mysql errors can quote parts of the failed SQL. Never print the raw
        # stderr because account SQL can contain credentials.
        printf 'Ares MySQL 初始化警告：持锁数据库会话执行 SQL 失败（错误详情已脱敏）\n' >&2
        return 1
    fi
    printf '%s' "$output"
}

start_root_mysql_session() {
    local hold_seconds="$1"

    [[ "$root_session_ready" != true ]] || return 1
    [[ "$hold_seconds" =~ ^[1-9][0-9]*$ ]] || return 1
    account_lock_directory="$(mktemp -d "${TMPDIR:-/tmp}/ares-mysql-account-lock.XXXXXX")" || \
        return 1
    if ! mkfifo "$account_lock_directory/input" "$account_lock_directory/output"; then
        rm -f -- "$account_lock_directory/input" "$account_lock_directory/output"
        rmdir -- "$account_lock_directory" >/dev/null 2>&1 || true
        account_lock_directory=""
        return 1
    fi

    # --skip-reconnect is part of the locking protocol: a transparent reconnect
    # would lose GET_LOCK while leaving this process able to issue account DDL.
    # --binary-mode/--skip-commands keep password backslashes as ordinary bytes.
    MYSQL_PWD="$MYSQL_ROOT_PASSWORD" timeout "${hold_seconds}s" "${mysql_command[@]}" \
        --batch --skip-column-names --raw --unbuffered --force \
        --skip-reconnect --binary-mode --skip-commands \
        <"$account_lock_directory/input" \
        >"$account_lock_directory/output" \
        2>"$account_lock_directory/error" &
    account_lock_holder_pid=$!

    # Open the FIFO ends in protocol order. The background redirections open
    # input first, so neither side can deadlock while establishing the channel.
    if ! exec 8>"$account_lock_directory/input"; then
        abort_root_mysql_session
        return 1
    fi
    root_session_input_open=true
    if ! exec 9<"$account_lock_directory/output"; then
        abort_root_mysql_session
        return 1
    fi
    root_session_output_open=true
    root_session_ready=true
}

run_mysql_with_password() {
    local password="$1"
    shift
    local now remaining status argument statement="" root_request=false
    local statement_provided=false

    now="$SECONDS"
    remaining=$((account_init_deadline - now))
    ((remaining > 0)) || fail "账号初始化超过 ${account_init_timeout_seconds} 秒总时限"

    for argument in "$@"; do
        [[ "$argument" == --user=root ]] && root_request=true
        if [[ "$argument" == --execute=* ]]; then
            statement="${argument#--execute=}"
            statement_provided=true
        fi
    done
    if [[ "$root_request" == true && "$root_session_ready" == true ]]; then
        if [[ "$statement_provided" != true ]]; then
            statement="$(command cat)"
        fi
        if root_mysql_batch "$statement" "$remaining"; then
            return 0
        else
            status=$?
        fi
    elif MYSQL_PWD="$password" timeout "${remaining}s" "$@"; then
        return 0
    else
        status=$?
    fi
    if ((status == 124 || status == 137 || status == 143)); then
        fail "账号初始化超过 ${account_init_timeout_seconds} 秒总时限"
    fi
    return "$status"
}

cleanup_mysql_execute() {
    root_mysql_batch "$1" 10
}

fail_closed_containment() {
    local cleanup_failed=false
    local tombstone_password session_ids session_id containment_state
    local containment_deadline=$((SECONDS + 30))
    local session_backoff=0.025

    if ! ensure_account_lock_for_containment; then
        printf 'Ares MySQL 初始化警告：未能持有账号级互斥锁，跳过账号和会话修改以避免影响后续作业\n' >&2
        return 1
    fi
    printf 'Ares MySQL 初始化异常：正在对账号 %s 执行 fail-closed 收敛\n' \
        "$account_user" >&2

    if ! cleanup_mysql_execute \
        "ALTER USER IF EXISTS '${account_user}'@'%' ACCOUNT LOCK" >/dev/null 2>&1; then
        cleanup_failed=true
        printf 'Ares MySQL 初始化警告：无法立即锁定账号 %s\n' "$account_user" >&2
    fi

    tombstone_password="Aa1!$(LC_ALL=C od -An -N32 -tx1 /dev/urandom 2>/dev/null | tr -d ' \n')"
    if [[ "$tombstone_password" =~ ^Aa1\![0-9a-f]{64}$ ]]; then
        if ! cleanup_mysql_execute \
            "ALTER USER IF EXISTS '${account_user}'@'%' IDENTIFIED BY '${tombstone_password}' ACCOUNT LOCK" \
            >/dev/null 2>&1; then
            cleanup_failed=true
            printf 'Ares MySQL 初始化警告：无法轮换账号 %s 的故障收敛凭据\n' \
                "$account_user" >&2
        fi
    else
        cleanup_failed=true
        printf 'Ares MySQL 初始化警告：无法生成账号 %s 的故障收敛凭据\n' \
            "$account_user" >&2
    fi

    if ! cleanup_mysql_execute \
        "ALTER USER IF EXISTS '${account_user}'@'%' DISCARD OLD PASSWORD" >/dev/null 2>&1; then
        cleanup_failed=true
        printf 'Ares MySQL 初始化警告：无法丢弃账号 %s 的旧凭据\n' "$account_user" >&2
    fi

    while ((SECONDS < containment_deadline)); do
        if ! session_ids="$(root_mysql_batch \
            "SELECT ID FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY '${account_user}' ORDER BY ID" \
            10)"; then
            cleanup_failed=true
            printf 'Ares MySQL 初始化警告：无法列出账号 %s 的残留会话\n' \
                "$account_user" >&2
            break
        fi
        [[ -n "$session_ids" ]] || break
        while IFS= read -r session_id; do
            [[ -n "$session_id" ]] || continue
            if [[ ! "$session_id" =~ ^[0-9]+$ ]]; then
                cleanup_failed=true
                printf 'Ares MySQL 初始化警告：账号 %s 的残留会话编号不可识别\n' \
                    "$account_user" >&2
                continue
            fi
            if ! cleanup_mysql_execute "KILL CONNECTION ${session_id}" >/dev/null 2>&1; then
                # A racing disconnect yields ER_NO_SUCH_THREAD and is harmless;
                # the final state query below remains authoritative.
                true
            fi
        done <<< "$session_ids"
        sleep "$session_backoff"
        case "$session_backoff" in
            0.025) session_backoff=0.05 ;;
            0.05) session_backoff=0.1 ;;
            *) session_backoff=0.2 ;;
        esac
    done

    if ! containment_state="$(root_mysql_batch "SELECT
            (SELECT COUNT(*) FROM mysql.user WHERE BINARY User = BINARY '${account_user}' AND Host = '%'),
            COALESCE((SELECT account_locked FROM mysql.user WHERE BINARY User = BINARY '${account_user}' AND Host = '%'), 'Y'),
            (SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY '${account_user}')" \
        10)"; then
        cleanup_failed=true
        printf 'Ares MySQL 初始化警告：无法验证账号 %s 的故障收敛状态\n' \
            "$account_user" >&2
    elif [[ "$containment_state" != $'0\tY\t0' && "$containment_state" != $'1\tY\t0' ]]; then
        cleanup_failed=true
        printf 'Ares MySQL 初始化警告：账号 %s 未可靠收敛（state=%s）\n' \
            "$account_user" "$containment_state" >&2
    fi

    [[ "$cleanup_failed" == false ]]
}

release_account_lock() {
    local release_failed=false owner release_result lock_name
    local index
    local lock_names_to_verify=("${acquired_account_lock_names[@]}")

    if [[ "$root_session_ready" == true ]]; then
        for ((index = ${#acquired_account_lock_names[@]} - 1; index >= 0; index--)); do
            lock_name="${acquired_account_lock_names[$index]}"
            if ! release_result="$(root_mysql_batch \
                "SELECT RELEASE_LOCK('${lock_name}')" 10)" || [[ "$release_result" != 1 ]]; then
                release_failed=true
            fi
        done
    fi
    account_lock_acquired=false

    if [[ "$root_session_input_open" == true ]]; then
        exec 8>&- || true
        root_session_input_open=false
    fi
    if [[ -n "$account_lock_holder_pid" ]] && \
        kill -0 "$account_lock_holder_pid" >/dev/null 2>&1; then
        for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
            kill -0 "$account_lock_holder_pid" >/dev/null 2>&1 || break
            sleep 0.05
        done
        if kill -0 "$account_lock_holder_pid" >/dev/null 2>&1; then
            kill "$account_lock_holder_pid" >/dev/null 2>&1 || true
            sleep 0.1
        fi
        if kill -0 "$account_lock_holder_pid" >/dev/null 2>&1; then
            kill -KILL "$account_lock_holder_pid" >/dev/null 2>&1 || true
        fi
        wait "$account_lock_holder_pid" >/dev/null 2>&1 || true
    elif [[ -n "$account_lock_holder_pid" ]]; then
        wait "$account_lock_holder_pid" >/dev/null 2>&1 || true
    fi
    if [[ "$root_session_output_open" == true ]]; then
        exec 9<&- || true
        root_session_output_open=false
    fi
    root_session_ready=false

    if [[ -n "$account_lock_connection_id" ]]; then
        for lock_name in "${lock_names_to_verify[@]}"; do
            if ! owner="$(emergency_mysql --batch --skip-column-names --raw \
                --execute="SELECT IS_USED_LOCK('${lock_name}')" 2>/dev/null)"; then
                owner=""
                release_failed=true
            fi
            if [[ "$owner" == "$account_lock_connection_id" ]]; then
                emergency_mysql --batch --skip-column-names --raw \
                    --execute="KILL CONNECTION ${account_lock_connection_id}" \
                    >/dev/null 2>&1 || true
                owner="$(emergency_mysql --batch --skip-column-names --raw \
                    --execute="SELECT IS_USED_LOCK('${lock_name}')" 2>/dev/null)" || owner=""
            fi
            if [[ "$owner" == "$account_lock_connection_id" ]]; then
                release_failed=true
                printf 'Ares MySQL 初始化警告：账号级互斥锁 %s 仍由连接 %s 持有\n' \
                    "$lock_name" "$account_lock_connection_id" >&2
            fi
        done
    fi

    if [[ -n "$account_lock_directory" ]]; then
        rm -f -- "$account_lock_directory/input" "$account_lock_directory/output" \
            "$account_lock_directory/error"
        rmdir -- "$account_lock_directory" >/dev/null 2>&1 || release_failed=true
    fi
    if [[ "$release_failed" == true ]]; then
        printf 'Ares MySQL 初始化警告：账号级互斥锁连接未能可靠释放或验证\n' >&2
    fi
    account_lock_names=()
    acquired_account_lock_names=()
    account_lock_connection_id=""
    account_lock_holder_pid=""
    account_lock_directory=""
    account_lock_acquired=false
    [[ "$release_failed" == false ]]
}

ensure_account_lock_for_containment() {
    local owner lock_name lock_result
    local candidate_connection_id acquired unexpected_lock_fields
    local desired_lock_names=("${account_lock_names[@]}")
    local all_owned=true

    [[ "$account_lock_acquired" == true && \
        ${#acquired_account_lock_names[@]} -eq ${#account_lock_names[@]} ]] || all_owned=false
    if [[ "$all_owned" == true ]]; then
        for lock_name in "${account_lock_names[@]}"; do
            if ! owner="$(root_mysql_batch "SELECT IS_USED_LOCK('${lock_name}')" 10)" || \
                [[ "$owner" != "$account_lock_connection_id" ]]; then
                all_owned=false
                break
            fi
        done
    fi
    if [[ "$all_owned" == true ]]; then
        return 0
    fi

    printf 'Ares MySQL 初始化警告：原账号级互斥锁已丢失，收敛前尝试重新获取\n' >&2
    release_account_lock >/dev/null 2>&1 || true
    account_lock_names=("${desired_lock_names[@]}")

    # Never queue a stale cleanup behind a newer account job. If another owner
    # has already taken over, that owner is responsible for its own containment.
    if ! start_root_mysql_session 120; then
        printf 'Ares MySQL 初始化警告：无法创建故障收敛锁连接\n' >&2
        return 1
    fi
    for lock_name in "${account_lock_names[@]}"; do
        if ! lock_result="$(root_mysql_batch \
            "SELECT CONNECTION_ID(), GET_LOCK('${lock_name}', 0)" 10)"; then
            printf 'Ares MySQL 初始化警告：无法立即获取故障收敛账号锁\n' >&2
            release_account_lock >/dev/null 2>&1 || true
            return 1
        fi
        IFS=$'\t' read -r candidate_connection_id acquired unexpected_lock_fields <<< "$lock_result"
        if [[ ! "$candidate_connection_id" =~ ^[0-9]+$ || -n "$unexpected_lock_fields" ]]; then
            printf 'Ares MySQL 初始化警告：故障收敛锁结果格式不可识别\n' >&2
            release_account_lock >/dev/null 2>&1 || true
            return 1
        fi
        if [[ -z "$account_lock_connection_id" ]]; then
            account_lock_connection_id="$candidate_connection_id"
        elif [[ "$candidate_connection_id" != "$account_lock_connection_id" ]]; then
            printf 'Ares MySQL 初始化警告：故障收敛锁物理连接发生变化\n' >&2
            release_account_lock >/dev/null 2>&1 || true
            return 1
        fi
        if [[ "$acquired" != 1 ]]; then
            printf 'Ares MySQL 初始化警告：故障收敛账号锁已被后续作业持有，跳过过期收敛\n' >&2
            release_account_lock >/dev/null 2>&1 || true
            return 1
        fi
        acquired_account_lock_names+=("$lock_name")
    done
    account_lock_acquired=true
    for lock_name in "${account_lock_names[@]}"; do
        if ! owner="$(root_mysql_batch "SELECT IS_USED_LOCK('${lock_name}')" 10)" || \
            [[ "$owner" != "$account_lock_connection_id" ]]; then
            printf 'Ares MySQL 初始化警告：重新获取的故障收敛账号锁所有权不匹配\n' >&2
            release_account_lock >/dev/null 2>&1 || true
            return 1
        fi
    done
    return 0
}

handle_exit() {
    local status="$1"
    local cleanup_failed=false
    trap - EXIT
    set +e

    if [[ "$containment_armed" == true && "$task_succeeded" != true ]]; then
        fail_closed_containment || cleanup_failed=true
    fi
    release_account_lock || cleanup_failed=true
    if [[ "$cleanup_failed" == true ]]; then
        status=1
    fi
    exit "$status"
}

assert_account_lock_owned() {
    local owner lock_name
    [[ "$account_lock_acquired" == true && \
        ${#acquired_account_lock_names[@]} -eq ${#account_lock_names[@]} ]] || \
        fail '账号级互斥锁尚未全部获取'
    for lock_name in "${account_lock_names[@]}"; do
        owner="$(run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
            --batch --skip-column-names --raw \
            --execute="SELECT IS_USED_LOCK('${lock_name}')")" || \
            fail '无法验证账号级互斥锁所有权'
        [[ "$owner" == "$account_lock_connection_id" ]] || \
            fail '账号级互斥锁所有权已丢失，拒绝继续修改数据库账号'
    done
}

acquire_account_lock() {
    local remaining effective_lock_timeout lock_result lock_name
    local candidate_connection_id acquired unexpected_lock_fields

    for lock_name in "${account_lock_names[@]}"; do
        remaining=$((account_init_deadline - SECONDS))
        ((remaining > 0)) || fail "账号初始化超过 ${account_init_timeout_seconds} 秒总时限"
        effective_lock_timeout=$((10#$account_lock_timeout_seconds))
        if ((effective_lock_timeout > remaining)); then
            effective_lock_timeout=$remaining
        fi
        lock_result="$(root_mysql_batch \
            "SELECT CONNECTION_ID(), GET_LOCK('${lock_name}', ${effective_lock_timeout})" \
            "$remaining")" || fail '账号级互斥锁连接执行失败'
        IFS=$'\t' read -r candidate_connection_id acquired unexpected_lock_fields <<< "$lock_result"
        [[ "$candidate_connection_id" =~ ^[0-9]+$ ]] || \
            fail '账号级互斥锁连接编号不可识别'
        [[ -z "$unexpected_lock_fields" ]] || fail '账号级互斥锁结果格式不可识别'
        if [[ -z "$account_lock_connection_id" ]]; then
            account_lock_connection_id="$candidate_connection_id"
        else
            [[ "$candidate_connection_id" == "$account_lock_connection_id" ]] || \
                fail '账号级互斥锁未使用同一物理连接'
        fi
        if [[ "$acquired" == 1 ]]; then
            acquired_account_lock_names+=("$lock_name")
            continue
        fi
        [[ "$acquired" == 0 ]] || fail '账号级互斥锁返回了不可识别的状态'
        fail "等待账号级互斥锁超过 ${effective_lock_timeout} 秒"
    done
    account_lock_acquired=true
    assert_account_lock_owned
}

trap 'handle_exit $?' EXIT
initial_holder_seconds=$((account_init_deadline - SECONDS + 120))
((initial_holder_seconds > 120)) || fail '账号初始化总时限已耗尽，无法创建持锁数据库会话'
start_root_mysql_session "$initial_holder_seconds" || fail '无法创建持锁数据库会话'

server_identity="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT VERSION(), @@version_comment"
)"
[[ "$server_identity" != *$'\n'* ]] || fail '数据库版本查询结果格式不可识别'
IFS=$'\t' read -r server_version server_version_comment unexpected_server_fields <<< "$server_identity"
[[ "$server_version" =~ ^8\.4\.[0-9]+([.-].*)?$ ]] || fail \
    '数据库账号初始化仅支持 MySQL 8.4.x'
[[ "${server_version_comment,,}" != *mariadb* ]] || fail \
    '数据库账号初始化不支持 MariaDB'
[[ -z "$unexpected_server_fields" ]] || fail '数据库版本查询结果格式不可识别'

migration_account_lock_name="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT CONCAT('ares_migration_account_', LEFT(SHA2('${MYSQL_MIGRATION_USER}', 256), 32))"
)"
[[ "$migration_account_lock_name" =~ ^ares_migration_account_[0-9a-f]{32}$ ]] || \
    fail '账号级互斥锁名称不可识别'
((${#migration_account_lock_name} <= 64)) || fail '账号级互斥锁名称超过 MySQL 64 字节上限'
account_lock_names=("$migration_account_lock_name")
if [[ "$ARES_DATABASE_ACCOUNT_ROLE" == runtime ]]; then
    runtime_account_lock_name="$(
        run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
            --batch --skip-column-names --raw \
            --execute="SELECT CONCAT('ares_migration_account_', LEFT(SHA2('${MYSQL_RUNTIME_USER}', 256), 32))"
    )"
    [[ "$runtime_account_lock_name" =~ ^ares_migration_account_[0-9a-f]{32}$ ]] || \
        fail '运行时账号互斥锁名称不可识别'
    ((${#runtime_account_lock_name} <= 64)) || fail '运行时账号互斥锁名称超过 MySQL 64 字节上限'
    [[ "$runtime_account_lock_name" != "$migration_account_lock_name" ]] || \
        fail '运行时账号与迁移账号互斥锁不得相同'
    # A global bytewise order prevents cross-config deadlocks such as
    # (migration=A,runtime=B) racing (migration=B,runtime=A). Both guards are
    # held on this same physical session before any persistent write.
    sorted_account_lock_names="$(printf '%s\n%s\n' \
        "$migration_account_lock_name" "$runtime_account_lock_name" | LC_ALL=C sort -u)"
    account_lock_names=()
    while IFS= read -r sorted_lock_name; do
        [[ -n "$sorted_lock_name" ]] && account_lock_names+=("$sorted_lock_name")
    done <<< "$sorted_account_lock_names"
    [[ ${#account_lock_names[@]} -eq 2 ]] || fail '运行时账号互斥锁排序或去重失败'
fi
acquire_account_lock

run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
    --execute="SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES'"

partial_revokes_enabled="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT @@GLOBAL.partial_revokes"
)"
[[ "$partial_revokes_enabled" =~ ^[01]$ ]] || fail 'partial_revokes 状态不可识别'

administrator_capability_state="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT
            u.Process_priv,
            u.Create_user_priv,
            u.Super_priv,
            u.Select_priv,
            u.Trigger_priv,
            u.Event_priv,
            u.Show_view_priv,
            EXISTS(
                SELECT 1 FROM mysql.global_grants g
                WHERE BINARY g.USER = BINARY u.User
                    AND BINARY g.HOST = BINARY u.Host
                    AND g.PRIV = 'CONNECTION_ADMIN'
            ),
            COALESCE(JSON_LENGTH(JSON_EXTRACT(u.User_attributes, '$.Restrictions')), 0)
        FROM mysql.user u
        WHERE BINARY CONCAT(u.User, '@', u.Host) = BINARY CURRENT_USER()"
)"
[[ "$administrator_capability_state" != *$'\n'* ]] || fail '数据库管理身份能力查询结果格式不可识别'
IFS=$'\t' read -r administrator_process_priv administrator_create_user_priv \
    administrator_super_priv administrator_select_priv administrator_trigger_priv \
    administrator_event_priv administrator_show_view_priv administrator_connection_admin \
    administrator_restriction_count unexpected_administrator_capability_fields \
    <<< "$administrator_capability_state"
[[ "$administrator_process_priv" == Y && "$administrator_create_user_priv" == Y && \
    ("$administrator_super_priv" == Y || "$administrator_connection_admin" == 1) ]] || fail \
    "当前数据库管理身份缺少直接 PROCESS、CREATE USER 或 SUPER/CONNECTION_ADMIN 权限（state=${administrator_capability_state:-<空>}）；无法可信枚举并终止账号会话"
[[ "$administrator_select_priv" == Y && "$administrator_trigger_priv" == Y && \
    "$administrator_event_priv" == Y && "$administrator_show_view_priv" == Y && \
    "$administrator_restriction_count" == 0 && -z "$unexpected_administrator_capability_fields" ]] || fail \
    "当前数据库管理身份缺少直接全局 SELECT、TRIGGER、EVENT、SHOW VIEW 权限，存在部分权限限制或能力结果不可识别（state=${administrator_capability_state:-<空>}）；无法证明数据库元数据视图完整"

grant_database_pattern="$MYSQL_DATABASE"
if [[ "$partial_revokes_enabled" == 0 ]]; then
    # With partial_revokes=OFF mysql.db.Db is a LIKE pattern. Escape all three
    # pattern metacharacters even though current identifier validation only
    # permits underscores, so widening that validation cannot reopen this bug.
    grant_database_pattern="${grant_database_pattern//\\/\\\\}"
    grant_database_pattern="${grant_database_pattern//\%/\\%}"
    grant_database_pattern="${grant_database_pattern//_/\\_}"
fi
((${#grant_database_pattern} <= 64)) || fail \
    '数据库名转义后的授权 pattern 超过 MySQL 64 字节上限；请缩短含通配符字符的数据库名或由 DBA 评估启用 partial_revokes'

mandatory_roles="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT COALESCE(@@GLOBAL.mandatory_roles, '')"
)"
mandatory_roles_compact="${mandatory_roles//[[:space:]]/}"
[[ -z "$mandatory_roles_compact" ]] || fail \
    "数据库启用了 mandatory_roles（${mandatory_roles}），无法证明 Ares 账号的最小权限；请由 DBA 关闭该继承或手工建号并审计"

anonymous_account_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT COUNT(*) FROM mysql.user WHERE User = ''"
)"
[[ "$anonymous_account_count" == 0 ]] || fail \
    "数据库存在 ${anonymous_account_count} 个匿名账号，可能遮蔽 Ares 命名账号；请由 DBA 审计并移除后重试"

shadow_hosts="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT COALESCE(GROUP_CONCAT(QUOTE(Host) ORDER BY Host SEPARATOR ', '), '') FROM mysql.user WHERE User = '${account_user}' AND Host <> '%'"
)"
[[ -z "$shadow_hosts" ]] || fail \
    "存在可能遮蔽 '${account_user}'@'%' 的同名账号 Host=${shadow_hosts}；请由 DBA 审计并处理后重试"

outgoing_role_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT COUNT(*) FROM mysql.role_edges WHERE FROM_USER = '${account_user}' AND FROM_HOST = '%'"
)"
[[ "$outgoing_role_count" == 0 ]] || fail \
    "目标账号 ${account_user}@% 被用作数据库角色并已授予其他账号；请由 DBA 解除外部关系后重试"

outgoing_proxy_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT COUNT(*) FROM mysql.proxies_priv WHERE Proxied_user = '${account_user}' AND Proxied_host = '%'"
)"
[[ "$outgoing_proxy_count" == 0 ]] || fail \
    "目标账号 ${account_user}@% 正被其他账号代理；请由 DBA 解除外部关系后重试"

schema_executable_object_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT
            (SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = '${MYSQL_DATABASE}') +
            (SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = '${MYSQL_DATABASE}') +
            (SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = '${MYSQL_DATABASE}') +
            (SELECT COUNT(*) FROM information_schema.VIEWS WHERE TABLE_SCHEMA = '${MYSQL_DATABASE}')"
)"
[[ "$schema_executable_object_count" == 0 ]] || fail \
    "目标数据库 ${MYSQL_DATABASE} 已存在触发器、事件、存储程序或视图；无法安全收敛 Ares 账号权限"

account_definer_object_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT
            (SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE DEFINER = '${account_user}@%') +
            (SELECT COUNT(*) FROM information_schema.EVENTS WHERE DEFINER = '${account_user}@%') +
            (SELECT COUNT(*) FROM information_schema.ROUTINES WHERE DEFINER = '${account_user}@%') +
            (SELECT COUNT(*) FROM information_schema.VIEWS WHERE DEFINER = '${account_user}@%')"
)"
[[ "$account_definer_object_count" == 0 ]] || fail \
    "目标账号 ${account_user}@% 仍是数据库对象 DEFINER；请由 DBA 在所有数据库中解除外部依赖后重试"

target_external_privilege_state="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT
            (SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES
                WHERE GRANTEE = CONCAT(CHAR(39), '${account_user}', CHAR(39), '@', CHAR(39), '%', CHAR(39))
                    AND PRIVILEGE_TYPE <> 'USAGE'),
            (SELECT COUNT(*) FROM mysql.global_grants
                WHERE BINARY USER = BINARY '${account_user}' AND HOST = '%'),
            (SELECT COUNT(*) FROM mysql.db
                WHERE BINARY User = BINARY '${account_user}' AND Host = '%'
                    AND BINARY Db <> BINARY '${grant_database_pattern}'),
            (SELECT COUNT(*) FROM mysql.tables_priv
                WHERE BINARY User = BINARY '${account_user}' AND Host = '%'
                    AND BINARY Db <> BINARY '${MYSQL_DATABASE}'),
            (SELECT COUNT(*) FROM mysql.columns_priv
                WHERE BINARY User = BINARY '${account_user}' AND Host = '%'
                    AND BINARY Db <> BINARY '${MYSQL_DATABASE}'),
            (SELECT COUNT(*) FROM mysql.procs_priv
                WHERE BINARY User = BINARY '${account_user}' AND Host = '%'
                    AND BINARY Db <> BINARY '${MYSQL_DATABASE}')"
)"
[[ "$target_external_privilege_state" == $'0\t0\t0\t0\t0\t0' ]] || fail \
    "目标账号 ${account_user}@% 持有全局、其他数据库或外部对象权限（state=${target_external_privilege_state}）；拒绝跨部署复用账号，请由 DBA 独立建号并清理旧授权"

unexpected_schema_grantee_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT COUNT(*) FROM (
            SELECT d.User, d.Host FROM mysql.db d
            WHERE (${partial_revokes_enabled} = 1 AND BINARY d.Db = BINARY '${MYSQL_DATABASE}')
                OR (${partial_revokes_enabled} = 0 AND BINARY '${MYSQL_DATABASE}' LIKE BINARY d.Db ESCAPE CHAR(92))
            UNION ALL
            SELECT p.User, p.Host FROM mysql.tables_priv p
            WHERE BINARY p.Db = BINARY '${MYSQL_DATABASE}'
            UNION ALL
            SELECT p.User, p.Host FROM mysql.columns_priv p
            WHERE BINARY p.Db = BINARY '${MYSQL_DATABASE}'
            UNION ALL
            SELECT p.User, p.Host FROM mysql.procs_priv p
            WHERE BINARY p.Db = BINARY '${MYSQL_DATABASE}'
        ) schema_grants
        WHERE NOT (
            (BINARY User = BINARY '${MYSQL_RUNTIME_USER}' AND Host = '%')
            OR (BINARY User = BINARY '${MYSQL_MIGRATION_USER}' AND Host = '%')
        )"
)"
[[ "$unexpected_schema_grantee_count" == 0 ]] || fail \
    "目标数据库仍向 ${unexpected_schema_grantee_count} 个非 Ares 身份授权；请由 DBA 撤销旧版或未知主体授权后重试"

if [[ "$ARES_DATABASE_ACCOUNT_ROLE" == runtime ]]; then
    migration_account_state="$(
        run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
            --batch --skip-column-names --raw \
            --execute="SELECT u.account_locked,
                (SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE BINARY p.USER = BINARY '${MYSQL_MIGRATION_USER}')
            FROM mysql.user u
            WHERE u.User = '${MYSQL_MIGRATION_USER}' AND u.Host = '%'"
    )"
    [[ "$migration_account_state" == $'Y\t0' ]] || fail \
        '迁移账号未处于已锁定且无会话状态，拒绝配置运行时账号'
else
    migration_target_state="$(
        run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
            --batch --skip-column-names --raw \
            --execute="SELECT
                (SELECT COUNT(*) FROM mysql.user WHERE BINARY User = BINARY '${MYSQL_MIGRATION_USER}' AND Host = '%'),
                COALESCE((SELECT account_locked FROM mysql.user WHERE BINARY User = BINARY '${MYSQL_MIGRATION_USER}' AND Host = '%'), 'Y'),
                (SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY '${MYSQL_MIGRATION_USER}')"
    )"
    [[ "$migration_target_state" != *$'\n'* ]] || \
        fail '迁移账号状态查询结果格式不可识别'
    IFS=$'\t' read -r migration_target_count migration_target_locked \
        migration_target_sessions unexpected_migration_state_fields <<< "$migration_target_state"
    [[ "$migration_target_count" =~ ^[01]$ && "$migration_target_locked" =~ ^[YN]$ && \
        "$migration_target_sessions" =~ ^[0-9]+$ && -z "$unexpected_migration_state_fields" ]] || \
        fail '迁移账号状态查询结果格式不可识别'
    if [[ "$migration_target_count" == 0 && "$migration_target_sessions" != 0 ]]; then
        fail '迁移账号不存在但检测到同名会话，拒绝修改账号'
    fi
    if [[ "$migration_target_count" == 1 && "$migration_target_locked" == Y && \
        "$migration_target_sessions" != 0 ]]; then
        fail '检测到已锁定迁移账号仍有活跃会话，视为 guarded 迁移执行中并拒绝修改账号'
    fi
fi

containment_armed=true
assert_account_lock_owned
run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" <<SQL
SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES';

CREATE DATABASE IF NOT EXISTS \`${MYSQL_DATABASE}\`
    CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE USER IF NOT EXISTS '${account_user}'@'%' IDENTIFIED BY '${account_password_sql}' ACCOUNT LOCK;
ALTER USER '${account_user}'@'%' ACCOUNT LOCK;
ALTER USER '${account_user}'@'%' IDENTIFIED BY '${account_password_sql}';
ALTER USER '${account_user}'@'%' DISCARD OLD PASSWORD;
SET DEFAULT ROLE NONE TO '${account_user}'@'%';
SET SESSION group_concat_max_len = 1048576;
SELECT GROUP_CONCAT(
    CONCAT(
        CHAR(39), REPLACE(FROM_USER, CHAR(39), CONCAT(CHAR(39), CHAR(39))), CHAR(39),
        '@',
        CHAR(39), REPLACE(FROM_HOST, CHAR(39), CONCAT(CHAR(39), CHAR(39))), CHAR(39)
    )
    ORDER BY FROM_USER, FROM_HOST SEPARATOR ', '
) INTO @account_roles
FROM mysql.role_edges
WHERE TO_USER = '${account_user}' AND TO_HOST = '%';
SET @account_statement = IF(
    @account_roles IS NULL,
    'SET @role_cleanup_noop = 1',
    CONCAT('REVOKE ', @account_roles, ' FROM ''${account_user}''@''%''')
);
PREPARE account_statement FROM @account_statement;
EXECUTE account_statement;
DEALLOCATE PREPARE account_statement;
REVOKE ALL PRIVILEGES, GRANT OPTION FROM '${account_user}'@'%';

SET @account_statement = NULL;
SET @account_roles = NULL;
SQL

remaining_role_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names \
        --execute="SELECT COUNT(*) FROM mysql.role_edges WHERE TO_USER = '${account_user}' AND TO_HOST = '%'"
)"
[[ "$remaining_role_count" == 0 ]] || fail "账号 ${account_user} 仍绑定 ${remaining_role_count} 个数据库角色"

proxy_rows="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT CONCAT(HEX(Proxied_user), ':', HEX(Proxied_host)) FROM mysql.proxies_priv WHERE User = '${account_user}' AND Host = '%' ORDER BY Proxied_user, Proxied_host"
)"
while IFS= read -r proxy_row; do
    [[ -n "$proxy_row" ]] || continue
    proxied_user_hex="${proxy_row%%:*}"
    proxied_host_hex="${proxy_row#*:}"
    [[ "$proxy_row" == *:* ]] || fail "账号 ${account_user} 的 PROXY 授权格式不可识别"
    [[ "$proxied_user_hex" =~ ^[0-9A-F]*$ ]] || fail "账号 ${account_user} 的 PROXY 用户编码不可识别"
    [[ "$proxied_host_hex" =~ ^[0-9A-F]*$ ]] || fail "账号 ${account_user} 的 PROXY host 编码不可识别"

    assert_account_lock_owned
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" <<SQL
SET @proxied_user = CONVERT(X'${proxied_user_hex}' USING utf8mb4);
SET @proxied_host = CONVERT(X'${proxied_host_hex}' USING utf8mb4);
SET @account_statement = CONCAT(
    'REVOKE PROXY ON ',
    CHAR(39), REPLACE(@proxied_user, CHAR(39), CONCAT(CHAR(39), CHAR(39))), CHAR(39),
    '@',
    CHAR(39), REPLACE(@proxied_host, CHAR(39), CONCAT(CHAR(39), CHAR(39))), CHAR(39),
    ' FROM ''${account_user}''@''%'''
);
PREPARE account_statement FROM @account_statement;
EXECUTE account_statement;
DEALLOCATE PREPARE account_statement;
SET @proxied_user = NULL;
SET @proxied_host = NULL;
SET @account_statement = NULL;
SQL
done <<< "$proxy_rows"

remaining_proxy_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names \
        --execute="SELECT COUNT(*) FROM mysql.proxies_priv WHERE User = '${account_user}' AND Host = '%'"
)"
[[ "$remaining_proxy_count" == 0 ]] || fail "账号 ${account_user} 仍绑定 ${remaining_proxy_count} 个 PROXY 授权"

session_backoff=0.025
while true; do
    existing_session_ids="$(
        run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
            --batch --skip-column-names --raw \
            --execute="SELECT ID FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY '${account_user}' ORDER BY ID"
    )"
    [[ -n "$existing_session_ids" ]] || break
    while IFS= read -r session_id; do
        [[ -n "$session_id" ]] || continue
        [[ "$session_id" =~ ^[0-9]+$ ]] || fail "账号 ${account_user} 的现有会话编号格式不可识别"
        assert_account_lock_owned
        run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
            --batch --skip-column-names --raw \
            --execute="KILL CONNECTION ${session_id}" || true
    done <<< "$existing_session_ids"
    ((SECONDS < account_init_deadline)) || \
        fail "等待账号 ${account_user} 的旧会话退出超过账号初始化总时限"
    sleep "$session_backoff"
    case "$session_backoff" in
        0.025) session_backoff=0.05 ;;
        0.05) session_backoff=0.1 ;;
        *) session_backoff=0.2 ;;
    esac
done

remaining_session_count="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY '${account_user}'"
)"
[[ "$remaining_session_count" == 0 ]] || fail \
    "账号 ${account_user} 仍存在 ${remaining_session_count} 个旧会话，拒绝授予业务权限"

current_partial_revokes="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT @@GLOBAL.partial_revokes"
)"
[[ "$current_partial_revokes" == "$partial_revokes_enabled" ]] || fail \
    'partial_revokes 在账号初始化过程中发生变化，拒绝按不稳定的数据库授权语义继续'

if [[ "$ARES_DATABASE_ACCOUNT_ROLE" == migrator ]]; then
    assert_account_lock_owned
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" <<SQL
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES
    ON \`${grant_database_pattern}\`.* TO '${MYSQL_MIGRATION_USER}'@'%';
SQL
else
    assert_account_lock_owned
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" <<SQL
GRANT SELECT ON \`${grant_database_pattern}\`.* TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`apps\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`app_configs\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`app_config_domains\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`task_record\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`task_record_images\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`pipelines\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`pipelines_job_combination\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`env_configs\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`integration_settings\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`dev_language_rules\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`release_workflows\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`release_workflow_versions\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`app_config_workflows\` TO '${MYSQL_RUNTIME_USER}'@'%';
GRANT INSERT, UPDATE, DELETE ON \`${MYSQL_DATABASE}\`.\`task_step_records\` TO '${MYSQL_RUNTIME_USER}'@'%';
SQL
fi

schema_grant_pattern_state="$(
    run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
        --batch --skip-column-names --raw \
        --execute="SELECT @@GLOBAL.partial_revokes,
            SUM(BINARY Db = BINARY '${grant_database_pattern}'),
            SUM(BINARY Db <> BINARY '${grant_database_pattern}')
        FROM mysql.db
        WHERE BINARY User = BINARY '${account_user}' AND Host = '%'"
)"
[[ "$schema_grant_pattern_state" == "${partial_revokes_enabled}"$'\t1\t0' ]] || fail \
    "账号 ${account_user} 的数据库级授权 pattern 未安全收敛（state=${schema_grant_pattern_state}）"

if [[ "$ARES_DATABASE_ACCOUNT_ROLE" == migrator ]]; then
    migration_account_locked="$(
        run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
            --batch --skip-column-names --raw \
            --execute="SELECT account_locked FROM mysql.user WHERE User = '${account_user}' AND Host = '%'"
    )"
    [[ "$migration_account_locked" == Y ]] || fail '迁移账号授权后未保持锁定状态'
    assert_account_lock_owned
    printf 'Ares MySQL migrator 最小权限账号已配置并保持锁定\n'
    task_succeeded=true
    exit 0
fi

assert_account_lock_owned
run_mysql_with_password "$MYSQL_ROOT_PASSWORD" "${mysql_command[@]}" \
    --execute="ALTER USER '${account_user}'@'%' ACCOUNT UNLOCK"

if ! account_identity_and_roles="$(
    run_mysql_with_password "$account_password" mysql \
        --protocol=tcp \
        --host="$MYSQL_HOST" \
        --user="$account_user" \
        --database="$MYSQL_DATABASE" \
        --connect-timeout="$mysql_connect_timeout_seconds" \
        --batch --skip-column-names --raw \
        --execute="SET ROLE ALL; SELECT CURRENT_USER(), CURRENT_ROLE()"
)"; then
    fail "目标账号 ${account_user} 回连验证失败"
fi
if [[ "$account_identity_and_roles" == *$'\n'* ]]; then
    fail "目标账号回连结果格式不可识别"
fi
IFS=$'\t' read -r matched_account active_roles unexpected_fields <<< "$account_identity_and_roles"
if [[ "$matched_account" != "${account_user}@%" ]]; then
    fail "目标账号回连命中 ${matched_account:-<空>}，期望 ${account_user}@%"
fi
if [[ -n "$unexpected_fields" ]]; then
    fail "目标账号回连结果格式不可识别"
fi
if [[ "$active_roles" != NONE ]]; then
    fail "账号 ${account_user} 仍可激活数据库角色：${active_roles:-<空>}"
fi

assert_account_lock_owned
printf 'Ares MySQL %s 最小权限账号初始化完成\n' "$ARES_DATABASE_ACCOUNT_ROLE"
task_succeeded=true
