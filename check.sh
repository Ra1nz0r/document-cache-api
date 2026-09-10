#!/usr/bin/env bash

set -Eeuo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

BASE_URL="${BASE_URL:-http://localhost:8080}"
BASE_URL="${BASE_URL%/}"
ADMIN_TOKEN="${ADMIN_TOKEN:-local-dev-admin-token}"

# При необходимости можно явно указать пакет main.
APP_PACKAGE="${APP_PACKAGE:-}"

SERVER_PID=""
OWNER_TOKEN=""
READER_TOKEN=""
OUTSIDER_TOKEN=""
DOCUMENT_IDS=()
CHECKS=0

WORK_DIR="$(mktemp -d)"
SERVER_LOG="$WORK_DIR/server.log"
HEADERS="$WORK_DIR/headers"
BODY="$WORK_DIR/body"

info() {
    printf '\n%s\n' "$*"
}

fail() {
    printf '\nFAIL: %s\n' "$*" >&2
    exit 1
}

pass() {
    CHECKS=$((CHECKS + 1))
    printf 'OK: %s\n' "$*"
}

cleanup() {
    local exit_code=$?
    trap - EXIT
    set +e

    # Удаляем оставшиеся документы, пока сессия владельца действительна.
    if [[ -n "$OWNER_TOKEN" ]]; then
        for id in "${DOCUMENT_IDS[@]}"; do
            curl -sS --max-time 5 \
                -X DELETE \
                --get \
                --data-urlencode "token=$OWNER_TOKEN" \
                "$BASE_URL/api/docs/$id" \
                >/dev/null 2>&1
        done
    fi

    for token in "$OWNER_TOKEN" "$READER_TOKEN" "$OUTSIDER_TOKEN"; do
        if [[ -n "$token" ]]; then
            curl -sS --max-time 5 \
                -X DELETE "$BASE_URL/api/auth/$token" \
                >/dev/null 2>&1
        fi
    done

    # Останавливаем только процесс, запущенный этим скриптом.
    if [[ -n "$SERVER_PID" ]]; then
        if kill -0 "$SERVER_PID" 2>/dev/null; then
            kill -TERM "$SERVER_PID" 2>/dev/null

            for ((i = 0; i < 15; i++)); do
                if ! kill -0 "$SERVER_PID" 2>/dev/null; then
                    break
                fi
                sleep 1
            done

            if kill -0 "$SERVER_PID" 2>/dev/null; then
                kill -KILL "$SERVER_PID" 2>/dev/null
            fi
        fi

        wait "$SERVER_PID" 2>/dev/null
    fi

    if [[ "$exit_code" -eq 0 ]]; then
        rm -rf -- "$WORK_DIR"
    else
        printf '\nДиагностика сохранена в: %s\n' "$WORK_DIR" >&2

        if [[ -s "$SERVER_LOG" ]]; then
            printf '\nПоследние строки лога сервера:\n' >&2
            tail -n 40 "$SERVER_LOG" >&2
        fi
    fi

    exit "$exit_code"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for command in go docker curl jq; do
    command -v "$command" >/dev/null 2>&1 ||
        fail "не найдена команда $command"
done

docker compose version >/dev/null 2>&1 ||
    fail "docker compose недоступен"

[[ -f docker-compose.yaml ]] ||
    fail "в корне не найден docker-compose.yaml"

# Сохраняет статус, заголовки и тело последнего ответа.
request() {
    local expected_status="$1"
    local method="$2"
    local path="$3"
    shift 3

    local status

    if ! status="$(
        curl -sS \
            --connect-timeout 3 \
            --max-time 20 \
            -X "$method" \
            -D "$HEADERS" \
            -o "$BODY" \
            -w '%{http_code}' \
            "$BASE_URL$path" \
            "$@"
    )"; then
        fail "$method $path: запрос не выполнен"
    fi

    if [[ "$status" != "$expected_status" ]]; then
        # Тело не печатаем автоматически: ответ auth может содержать токен.
        fail "$method $path: ожидался HTTP $expected_status, получен $status"
    fi
}

assert_json() {
    local expression="$1"
    shift

    jq -e "$@" "$expression" "$BODY" >/dev/null ||
        fail "JSON ответа не соответствует проверке: $expression"
}

header_value() {
    local name="$1"

    awk -v name="$name" '
        {
            sub(/\r$/, "")
            position = index($0, ":")
            if (position > 0 &&
                tolower(substr($0, 1, position - 1)) == tolower(name)) {
                value = substr($0, position + 1)
                sub(/^[ \t]+/, "", value)
                print value
                exit
            }
        }
    ' "$HEADERS"
}

assert_header() {
    local name="$1"
    local expected="$2"
    local actual

    actual="$(header_value "$name")"

    [[ "$actual" == "$expected" ]] ||
        fail "$name: ожидалось '$expected', получено '$actual'"
}

get_document() {
    local status="$1"
    local id="$2"
    local token="$3"

    request "$status" GET "/api/docs/$id" \
        --get --data-urlencode "token=$token"
}

list_documents() {
    local status="$1"
    local token="$2"
    shift 2

    request "$status" GET /api/docs \
        --get --data-urlencode "token=$token" "$@"
}

delete_document() {
    local status="$1"
    local id="$2"
    local token="$3"

    request "$status" DELETE "/api/docs/$id" \
        --get --data-urlencode "token=$token"
}

login() {
    local login_name="$1"

    request 200 POST /api/auth \
        --data-urlencode "login=$login_name" \
        --data-urlencode 'pswd=Checkpass1!'

    assert_json '.response.token | test("^[0-9a-f]{64}$")'
    assert_header Cache-Control no-store
}

# Находит документ по уникальному имени в коллекции владельца.
find_document() {
    local name="$1"

    list_documents 200 "$OWNER_TOKEN" \
        --data-urlencode 'key=name' \
        --data-urlencode "value=$name"

    assert_json '.data.docs | length == 1'
    FOUND_ID="$(jq -r '.data.docs[0].id' "$BODY")"
    DOCUMENT_IDS+=("$FOUND_ID")
}

upload_json() {
    local name="$1"
    local public="$2"
    local grant_json="$3"
    local content="$4"
    local meta

    meta="$(
        jq -cn \
            --arg name "$name" \
            --arg token "$OWNER_TOKEN" \
            --argjson public "$public" \
            --argjson grant "$grant_json" \
            '{
                name: $name,
                file: false,
                public: $public,
                token: $token,
                mime: "application/json",
                grant: $grant
            }'
    )"

    request 200 POST /api/docs \
        --form-string "meta=$meta" \
        --form-string "json=$content"

    assert_json '.data.json == $expected' --argjson expected "$content"
}

check_head() {
    local id="$1"
    local expected_length="$2"
    local expected_type="$3"
    local status

    if ! status="$(
        curl -sS \
            --connect-timeout 3 \
            --max-time 20 \
            --head \
            --get \
            --data-urlencode "token=$OWNER_TOKEN" \
            -D "$HEADERS" \
            -o /dev/null \
            -w '%{http_code}' \
            "$BASE_URL/api/docs/$id"
    )"; then
        fail "HEAD документа не выполнен"
    fi

    [[ "$status" == 200 ]] || fail "HEAD: ожидался 200, получен $status"
    assert_header X-Cache HIT
    assert_header Content-Length "$expected_length"
    assert_header Content-Type "$expected_type"
}

info "Проверяем, что по BASE_URL ещё не работает HTTP-сервер"

# Любой HTTP-ответ означает, что адрес уже обслуживается.
existing_status="$(
    curl -s --connect-timeout 2 --max-time 2 \
        -o /dev/null -w '%{http_code}' "$BASE_URL/api/docs" || true
)"

[[ "$existing_status" == "000" ]] ||
    fail "$BASE_URL уже отвечает. Останови сервер перед запуском проверки."

info "Поднимаем PostgreSQL и применяем миграции"

docker compose up -d postgres
docker compose run --rm migrate up

info "Определяем пакет приложения"

if [[ -z "$APP_PACKAGE" ]]; then
    main_packages="$(
        go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./...
    )"

    MAIN_PACKAGES=()
    while IFS= read -r package; do
        [[ -z "$package" ]] || MAIN_PACKAGES+=("$package")
    done <<< "$main_packages"

    [[ "${#MAIN_PACKAGES[@]}" -eq 1 ]] ||
        fail "найдено не ровно одно main-приложение; укажи APP_PACKAGE"

    APP_PACKAGE="${MAIN_PACKAGES[0]}"
fi

info "Собираем и запускаем сервер"

go build -o "$WORK_DIR/server" "$APP_PACKAGE"

"$WORK_DIR/server" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

ready=false

for ((i = 0; i < 40; i++)); do
    kill -0 "$SERVER_PID" 2>/dev/null ||
        fail "сервер завершился при запуске"

    status="$(
        curl -s --connect-timeout 1 --max-time 1 \
            -o /dev/null -w '%{http_code}' "$BASE_URL/api/docs" || true
    )"

    if [[ "$status" == 401 ]]; then
        ready=true
        break
    fi

    sleep 0.25
done

[[ "$ready" == true ]] ||
    fail "сервер не стал доступен по $BASE_URL"

kill -0 "$SERVER_PID" 2>/dev/null ||
    fail "запущенный процесс сервера завершился"

# Уникальные логины позволяют повторно запускать скрипт в той же БД.
RUN_ID="$(date +%s)$$"
OWNER_LOGIN="owner${RUN_ID}"
READER_LOGIN="reader${RUN_ID}"
OUTSIDER_LOGIN="other${RUN_ID}"

PRIVATE_NAME="private-${RUN_ID}.json"
SHARED_NAME="shared-${RUN_ID}.json"
PUBLIC_NAME="public-${RUN_ID}.json"
FILE_NAME="file-${RUN_ID}.txt"

info "Проверяем регистрацию и авторизацию"

request 401 POST /api/register \
    --data-urlencode 'token=wrong-admin-token' \
    --data-urlencode "login=$OWNER_LOGIN" \
    --data-urlencode 'pswd=Checkpass1!'
pass "регистрация с неверным admin token запрещена"

for login_name in "$OWNER_LOGIN" "$READER_LOGIN" "$OUTSIDER_LOGIN"; do
    request 200 POST /api/register \
        --data-urlencode "token=$ADMIN_TOKEN" \
        --data-urlencode "login=$login_name" \
        --data-urlencode 'pswd=Checkpass1!'

    assert_json '.response.login == $login' --arg login "$login_name"
done
pass "три пользователя зарегистрированы"

request 400 POST /api/register \
    --data-urlencode "token=$ADMIN_TOKEN" \
    --data-urlencode "login=$OWNER_LOGIN" \
    --data-urlencode 'pswd=Checkpass1!'
pass "повторная регистрация логина запрещена"

request 401 POST /api/auth \
    --data-urlencode "login=$OWNER_LOGIN" \
    --data-urlencode 'pswd=Wrongpass1!'
pass "неверный пароль отклонён"

login "$OWNER_LOGIN"
OWNER_TOKEN="$(jq -r '.response.token' "$BODY")"

login "$READER_LOGIN"
READER_TOKEN="$(jq -r '.response.token' "$BODY")"

login "$OUTSIDER_LOGIN"
OUTSIDER_TOKEN="$(jq -r '.response.token' "$BODY")"
pass "login выдаёт session tokens"

request 401 GET /api/docs
pass "список без сессии недоступен"

info "Проверяем неподдерживаемые HTTP-методы"

request 405 PUT /api/docs
assert_header Content-Type "application/json; charset=utf-8"
assert_header Allow "GET, HEAD, POST"
assert_json '.error.code == 405 and .error.text == "method not allowed"'

request 405 GET /api/register
assert_header Allow "POST"
assert_json '.error.code == 405'

request 405 POST /api/docs/00000000-0000-0000-0000-000000000000
assert_header Allow "GET, HEAD, DELETE"
assert_json '.error.code == 405'

pass "неподдерживаемые методы возвращают JSON-ошибку и Allow"

info "Проверяем загрузку и инвалидацию списка"

list_documents 200 "$OWNER_TOKEN"
assert_header X-Cache MISS
assert_json '.data.docs == []'

list_documents 200 "$OWNER_TOKEN"
assert_header X-Cache HIT
pass "пустой список кешируется"

upload_json "$PRIVATE_NAME" false '[]' '{"text":"private document"}'

list_documents 200 "$OWNER_TOKEN"
assert_header X-Cache MISS
assert_json '.data.docs | length == 1'
PRIVATE_ID="$(jq -r '.data.docs[0].id' "$BODY")"
DOCUMENT_IDS+=("$PRIVATE_ID")
pass "загрузка JSON инвалидирует список"

get_document 200 "$PRIVATE_ID" "$OWNER_TOKEN"
assert_header X-Cache MISS
assert_json '.data.text == "private document"'

get_document 200 "$PRIVATE_ID" "$OWNER_TOKEN"
assert_header X-Cache HIT
assert_json '.data.text == "private document"'

json_length="$(wc -c < "$BODY" | tr -d '[:space:]')"
check_head "$PRIVATE_ID" "$json_length" "application/json; charset=utf-8"
pass "JSON GET и HEAD используют общий кеш"

get_document 403 "$PRIVATE_ID" "$READER_TOKEN"
pass "кеш владельца не раскрывает приватный документ другому пользователю"

info "Проверяем grant и public"

grant_json="$(jq -cn --arg login "$READER_LOGIN" '[$login]')"

upload_json "$SHARED_NAME" false "$grant_json" '{"text":"shared document"}'
find_document "$SHARED_NAME"
SHARED_ID="$FOUND_ID"

upload_json "$PUBLIC_NAME" true '[]' '{"text":"public document"}'
find_document "$PUBLIC_NAME"
PUBLIC_ID="$FOUND_ID"

get_document 200 "$SHARED_ID" "$READER_TOKEN"
assert_json '.data.text == "shared document"'

get_document 403 "$SHARED_ID" "$OUTSIDER_TOKEN"

get_document 200 "$PUBLIC_ID" "$OUTSIDER_TOKEN"
assert_json '.data.text == "public document"'

request 401 GET "/api/docs/$PUBLIC_ID"
pass "grant и public разрешают чтение только нужным пользователям"

list_documents 200 "$READER_TOKEN" \
    --data-urlencode "login=$OWNER_LOGIN"

assert_json \
    '([.data.docs[].id] | sort) == ([$shared, $public] | sort)' \
    --arg shared "$SHARED_ID" \
    --arg public "$PUBLIC_ID"
pass "чужая коллекция содержит только доступные документы"

delete_document 403 "$SHARED_ID" "$READER_TOKEN"
delete_document 403 "$PUBLIC_ID" "$OUTSIDER_TOKEN"
get_document 200 "$SHARED_ID" "$OWNER_TOKEN"
get_document 200 "$PUBLIC_ID" "$OWNER_TOKEN"
pass "grant и public не разрешают удаление"

info "Проверяем файлы и фильтры"

printf 'File content for API check.\nВторая строка.\n' > "$WORK_DIR/sample.txt"

meta="$(
    jq -cn \
        --arg name "$FILE_NAME" \
        --arg token "$OWNER_TOKEN" \
        '{
            name: $name,
            file: true,
            public: false,
            token: $token,
            mime: "text/plain",
            grant: []
        }'
)"

request 200 POST /api/docs \
    --form-string "meta=$meta" \
    --form "file=@$WORK_DIR/sample.txt"

assert_json '.data.file == $name' --arg name "$FILE_NAME"

find_document "$FILE_NAME"
FILE_ID="$FOUND_ID"

get_document 200 "$FILE_ID" "$OWNER_TOKEN"
assert_header X-Cache MISS
assert_header Content-Type text/plain
cmp -s "$WORK_DIR/sample.txt" "$BODY" ||
    fail "полученный файл отличается от загруженного"

get_document 200 "$FILE_ID" "$OWNER_TOKEN"
assert_header X-Cache HIT
cmp -s "$WORK_DIR/sample.txt" "$BODY" ||
    fail "файл из кеша отличается от загруженного"

file_length="$(wc -c < "$WORK_DIR/sample.txt" | tr -d '[:space:]')"
check_head "$FILE_ID" "$file_length" text/plain
pass "файл загружается и возвращается без изменения байтов; HEAD работает"

list_documents 200 "$OWNER_TOKEN" --data-urlencode 'limit=1'
assert_json '.data.docs | length == 1'

list_documents 200 "$OWNER_TOKEN" \
    --data-urlencode 'key=name' \
    --data-urlencode "value=$PRIVATE_NAME"
assert_json \
    '(.data.docs | length == 1) and (.data.docs[0].id == $id)' \
    --arg id "$PRIVATE_ID"

list_documents 400 "$OWNER_TOKEN" \
    --data-urlencode 'key=unknown' \
    --data-urlencode 'value=test'

get_document 400 not-a-uuid "$OWNER_TOKEN"
pass "фильтр, limit и проверка некорректных параметров работают"

info "Проверяем удаление и сброс кеша"

# Сначала гарантированно заполняем кеш удаляемого документа и списка.
get_document 200 "$PRIVATE_ID" "$OWNER_TOKEN"
get_document 200 "$PRIVATE_ID" "$OWNER_TOKEN"
assert_header X-Cache HIT

list_documents 200 "$OWNER_TOKEN"
list_documents 200 "$OWNER_TOKEN"
assert_header X-Cache HIT

delete_document 200 "$PRIVATE_ID" "$OWNER_TOKEN"
assert_json '.response[$id] == true' --arg id "$PRIVATE_ID"

get_document 404 "$PRIVATE_ID" "$OWNER_TOKEN"
delete_document 404 "$PRIVATE_ID" "$OWNER_TOKEN"

list_documents 200 "$OWNER_TOKEN"
assert_header X-Cache MISS
assert_json 'all(.data.docs[]; .id != $id)' --arg id "$PRIVATE_ID"
pass "удалённый документ исчезает из кеша и списка"

delete_document 200 "$FILE_ID" "$OWNER_TOKEN"
get_document 404 "$FILE_ID" "$OWNER_TOKEN"
pass "файловый документ удалён и недоступен через API"

# Удаление общего документа должно сбросить и копию другого пользователя.
get_document 200 "$SHARED_ID" "$READER_TOKEN"
get_document 200 "$SHARED_ID" "$READER_TOKEN"
assert_header X-Cache HIT

delete_document 200 "$SHARED_ID" "$OWNER_TOKEN"
get_document 404 "$SHARED_ID" "$READER_TOKEN"

delete_document 200 "$PUBLIC_ID" "$OWNER_TOKEN"
get_document 404 "$PUBLIC_ID" "$OUTSIDER_TOKEN"
pass "удаление сбрасывает пользовательские копии общего документа"

DOCUMENT_IDS=()

info "Проверяем logout при заполненном кеше"

list_documents 200 "$OWNER_TOKEN"
list_documents 200 "$OWNER_TOKEN"
assert_header X-Cache HIT
assert_json '.data.docs == []'

request 200 DELETE "/api/auth/$OWNER_TOKEN"
assert_json '.response[$token] == true' --arg token "$OWNER_TOKEN"

list_documents 401 "$OWNER_TOKEN"

request 200 DELETE "/api/auth/$OWNER_TOKEN"
OWNER_TOKEN=""
pass "logout отзывает сессию, повторный logout допустим"

info "Проверяем вход после logout"

login "$OWNER_LOGIN"
OWNER_TOKEN="$(jq -r '.response.token' "$BODY")"

list_documents 200 "$OWNER_TOKEN"
assert_header X-Cache HIT
assert_json '.data.docs == []'
pass "новая сессия пользователя может использовать прежний кеш"

printf '\nPASS: выполнено %d групп проверок API.\n' "$CHECKS"