# gostodian

홈랩 서버의 정기 점검을 자동화하는 도구.

홈랩 밖의 점검 머신에서 `gostodian run` 프로세스를 한 번 실행하면, 그 프로세스가 VPN과 SSH로 홈랩에 접속해 유지보수와 로그 수집을 하고, 이상 여부를 판정한 뒤 보고서를 만들고 MinIO에 올린다. 결과 알림은 점검 머신이 Discord로 직접 보낸다. 프로세스 하나가 끝나면 `gostodian run`만 종료되고, `gostodian` 프로그램 파일은 점검 머신에 남는다.

홈랩에는 게이트 `gostodian-gate`만 둔다. 게이트는 허용된 동사(작업 이름)만 통과시킨다.

알림을 홈랩 위에 두면 홈랩이 죽었을 때 알림도 같이 죽는다. 그래서 알림은 항상 점검 머신의 `gostodian run`이 보낸다.

## 개요

```
[점검 머신]                                         [홈랩]
  타이머 또는 수동 실행                                sshd
       │                                              │ authorized_keys: restrict,from=,command=
       ▼                                              ▼
  gostodian run ── VPN 확인                            gostodian-gate
       │         SSH (ed25519, known_hosts 고정) ──▶    허용 동사만 통과
       │           status → stop → update → reboot       │
       │           → collect-logs · resources            ▼
       │           → start(역순) → health            gostodian-svc
       │                                              sudo 고정 헬퍼
       ├─ 필터 · 리댁션 · 규칙 판정
       ├─ 보고서 (AI 실패 시 고정 템플릿)
       ├─ MinIO  reports/  logs/  collects/
       └─ Discord  시작 · 완료 · 실패 · 거부
```

`gostodian run`이 하는 일은 아래 순서다. 게이트를 한 번 부를 때의 제한 시간은 설정의 `timeouts.step`이다.

1. 설정과 시크릿 파일 경로를 읽고, 실행 ID로 이력 파일을 연다. Discord로 시작을 알린다.
2. `vpn.interface`가 있으면 그 인터페이스가 올라와 있는지 본다. 없으면 `vpn_required`로 실패한다.
3. SSH로 접속해 게이트 `status`(hostname, uptime, whoami)를 실행한다. 접속 실패는 `auth_failed`, `host_key_mismatch`, `host_unreachable`로 나뉘어 이력과 알림에 남는다.
4. `pipeline.docker_order` 순서로 서비스를 멈춘다. 헬퍼에서는 `down`으로 실행해, 재부팅 뒤에 서비스가 스스로 올라오지 않게 한다.
5. `update`를 시작하고 `update-status`가 `done` 또는 `failed`가 될 때까지 본다.
6. 재부팅 전 `boot_id`(부팅마다 바뀌는 값)를 저장하고 `reboot`를 호출한다. 지수 백오프(기다리는 시간을 늘려 가며 다시 접속)로 재접속한 뒤, `timeouts.reboot` 안에 `boot_id`가 바뀌었는지 확인한다.
7. `collect-logs`로 로그 종류 `dnf`, `boot`, `secure`, `fail2ban`을 읽고, `resources`로 RAM, CPU, Storage를 읽는다. 결과는 이력과 `logs/collects/<실행ID>/`에 남는다. 한 건이 실패해도 나머지 수집과 이후 기동은 이어가고, 빠진 항목은 실패로 기록한다.
8. 수집한 글을 카테고리 규칙으로 거르고, 리댁션(호스트명, IP, 계정, 토큰을 가림)한 뒤 규칙으로 이상 여부를 판정한다. 매칭이 0건인 카테고리는 「이상 없음」이다.
9. `docker_order`의 역순으로 서비스를 켜고 `health`로 컨테이너와 포트 Listening(접속을 받는 상태)을 본다.
10. 실행 ID로 보고서 초안을 만든다. 로그는 구조화된 필드로 넣고 경계를 명시한다. AI 호출이 실패하면 고정 템플릿으로 보고서를 쓴다. LLM 출력은 실행하지 않는다.
11. `health`가 성공한 뒤에만 MinIO로 올린다. `reports/`는 보고서, `logs/`는 실행 로그, `collects/`는 수집 아카이브이고, 셋은 같은 실행 ID다. 업로드 키는 PutObject만 한다. 암호화는 버킷 기본 암호화다.
12. MinIO가 안 떠 있거나 업로드가 실패하면 산출물은 로컬에 남기고 Discord로 알린다. 이미 끝난 기동은 되돌리지 않는다.
13. 완료 알림에 판정 요약과 보고서 위치를 넣는다. 이번 실행에서 게이트가 거부한 기록은 보고서에 남고, 거부가 있으면 완료 알림과 따로 알린다.
14. 이력 파일을 쓰고 `gostodian run` 프로세스가 종료된다.

수집이 아닌 단계가 실패하면 다음 단계로 가지 않고 실패를 알린다. 자동 롤백은 없다. 알림과 보고서에는 원문 로그의 비밀과 웹훅 주소, 키 본문을 넣지 않는다.

원격에서 실행하는 명령은 고정 목록뿐이다. 셸 문자열로 조립하지 않는다. 호스트 키 검증은 끄지 않는다. 시크릿 본문은 파일에만 두고, 환경 변수에는 그 파일 경로만 둔다.

## 프로그램 사용법

`gostodian run`은 접속에 성공하면 패키지 갱신과 재부팅까지 이어서 한다. 홈랩에서 실행하기 전에 점검 창을 잡아 둔다.

### 준비

| | |
|---|---|
| Go | 1.26.10 이상 (`go.mod` 기준) |
| Docker | Compose v2. 컨테이너로 실행할 때 |
| GNU Make | Windows에서는 WSL 또는 Git Bash |

```bash
git clone <repo> && cd gostodian
make build
mkdir -p secrets logs
cp config/config.yaml.example config/config.yaml
```

| 키 | 내용 |
|---|---|
| `server.host` / `port` / `user` | 홈랩 주소와 점검 계정 |
| `ssh.identity_file` / `known_hosts_file` | SSH 개인키, 홈랩 호스트 키 파일 경로 |
| `pipeline.docker_order` | 정지 순서. 기동은 역순. 서비스 이름은 코드에 두지 않고 여기에만 둔다 |
| `pipeline.timeouts.step` / `reboot` | 게이트 호출 1회 제한 시간(기본 10분), 재부팅 뒤 돌아오기까지 제한 시간(기본 15분) |
| `pipeline.filter` | 종류별 include/exclude, 같은 줄 합치기, 길이 상한. 상한을 비우면 262144글자. 넘치면 본문을 자르고 이력에 남긴다 |
| `pipeline.redact` | 로그에 더 나오는 호스트명과 계정. IP, 토큰, 웹훅 주소는 설정 없이 가린다 |
| `pipeline.rules` | 종류별 판정 문자열. 필터와 리댁션 뒤의 본문에서 맞으면 그 줄을 센다. 0줄이면 「이상 없음」 |
| `secrets.*_file` | 시크릿 파일 경로. compose의 `GOSTODIAN_*_FILE`이 있으면 그쪽이 우선 |
| `vpn.interface` | 비어 있으면 VPN 검사를 하지 않는다. 예: `wg0` |
| `report.api_url` / `model` | OpenAI 호환 Chat Completions base와 모델. 제공자 이름은 코드에 없음 |
| `report.template_file` | 보고서 뼈대 마크다운. 기본 `templates/report_template.md`. 내장하지 않음 |
| `report.timeout` / `max_input_runes` / `max_output_tokens` | AI 호출 한도. 실패하면 같은 템플릿으로 로컬 채움 |

보고서 API 주소와 모델 이름, MinIO 버킷과 프리픽스도 이 설정 파일에 둔다. 코드에 제공자 이름이나 버킷 이름을 넣지 않는다. 템플릿이 바뀌면 AI 요청에 전문과 캐시 초기화 지시를 넣고, 같으면 동일 형식으로 작성하라고만 한다.

```bash
./bin/gostodian config validate --config config/config.yaml
```

### 시크릿

시크릿은 값이 아니라 파일이다. 환경 변수에 값을 넣으면 `docker inspect`와 크래시 덤프에 남으므로, 환경 변수에는 경로만 둔다. `secrets/`는 git에 올리지 않는다.

| 파일 | 내용 |
|---|---|
| `secrets/id_ed25519` | 홈랩 접속용 SSH 개인키. 권한 `0600` |
| `secrets/known_hosts` | 홈랩 호스트 키. 필수이며 검증을 끄지 않는다 |
| `secrets/discord_webhook` | Discord 웹훅 주소 |
| `secrets/ai_api_key` | 보고서 초안용 API 키 |
| `secrets/s3_access_key`, `secrets/s3_secret_key` | MinIO PutObject 전용 키 |

```bash
./bin/gostodian key init --config config/config.yaml
ssh-keyscan -t ed25519 <홈랩호스트> >> secrets/known_hosts
```

`key init`은 키 파일이 이미 있으면 바꾸지 않는다. 인증에 실패해도 키를 다시 만들지 않는다.

### 홈랩

이 프로그램은 홈랩 SSH 권한을 가진다. 점검 머신이나 컨테이너가 탈취되면 홈랩 접근으로 이어지므로, 홈랩 권한은 아래만 연다. 헬퍼 스크립트는 이 저장소에 없고 홈랩에 따로 둔다.

1. 점검 계정의 `authorized_keys`에 한 줄만 둔다.

   ```
   restrict,from="<점검머신 VPN IP>",command="/usr/local/bin/gostodian-gate" ssh-ed25519 AAAA... gostodian
   ```

   `from=`이 점검 머신 VPN 주소가 아니면 sshd가 게이트를 시작하지 않는다. `command=` 때문에 클라이언트가 보낸 문자열은 `SSH_ORIGINAL_COMMAND`로만 전달되고, 실행되는 파일은 게이트뿐이다.

2. 방화벽은 VPN에서 22번만 허용한다. 점검 머신의 공개 주소에서 22번은 막는다.

3. `make build`로 만든 `bin/gostodian-gate`를 `/usr/local/bin/gostodian-gate`에 둔다.

4. 게이트는 `docker`, `dnf`, `systemd-run`을 sudo로 직접 부르지 않는다. sudoers에는 고정 헬퍼 `/usr/local/libexec/gostodian-svc`의 인자까지 고정된 줄만 등록한다.

   ```
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc down <서비스>
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc start <서비스>
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc update
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc reboot
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs dnf
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs boot
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs secure
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs fail2ban
   ```

   `update-status`, `health`, `resources`는 읽기만 하므로 sudo 없이 헬퍼를 부른다. `update` 헬퍼는 내부에서 일회성 작업으로 `dnf -y update`를 돌린다. `collect-logs` 뒤의 `dnf`는 패키지 갱신 로그라는 종류 이름이다. `dnf` 명령 자체는 sudoers에 없다.

5. 게이트의 서비스 이름 허용 목록과 `docker_order`는 같은 이름만 쓴다. 허용 목록이 비어 있으면 `docker_order`도 비워 둔다. MinIO에 쓰는 키 권한은 PutObject만 연다.

### 게이트 동사

게이트 입력은 `동사 [이름]` 한 줄이다. 목록 밖 동사, 허용 목록 밖 이름, 추가 인자는 실행하지 않고 거부한다.

| 동사 | 이름 | 하는 일 | 권한 |
|---|---|---|---|
| `status` | 없음 | hostname, uptime, whoami | 읽기 |
| `boot-id` | 없음 | `/proc/sys/kernel/random/boot_id` | 읽기 |
| `stop` | 필수 | 헬퍼 `down <이름>` | sudo 고정 줄 |
| `start` | 필수 | 헬퍼 `start <이름>` | sudo 고정 줄 |
| `update` | 없음 | 헬퍼 `update` | sudo 고정 줄 |
| `update-status` | 없음 | `running` / `done` / `failed` | 읽기 |
| `reboot` | 없음 | 헬퍼 `reboot` | sudo 고정 줄 |
| `health` | 없음 | 컨테이너가 떠 있는지, 포트가 Listening인지 | 읽기 |
| `collect-logs` | `dnf` `boot` `secure` `fail2ban` 중 하나 | 그 종류의 로그를 읽음 | sudo 고정 줄 |
| `resources` | 없음 | RAM, CPU, Storage | 읽기 |

### 실행

수동 실행과 스케줄 실행은 같은 점검 경로다. 스케줄은 점검 머신에 상시 프로세스를 두지 않고, 머신 타이머가 `gostodian run`을 한 번 실행한다.

```bash
./bin/gostodian run --config config/config.yaml
docker compose run --rm gostodian run --log-dir /var/lib/gostodian
```

컨테이너는 루트가 읽기 전용이고 사용자 `65532`로 돈다. 호스트의 `logs/`는 그 사용자가 쓸 수 있어야 한다. 이력은 컨테이너의 `/var/lib/gostodian`에 쓰이고, compose가 그 경로를 호스트 `logs/`에 연결한다.

다른 명령:

```bash
./bin/gostodian version
./bin/gostodian config validate --config config/config.yaml
./bin/gostodian key init --config config/config.yaml
```

## 디렉터리 구조

```
gostodian/
├── cmd/
│   ├── gostodian/            # 점검 머신 프로그램. version, config validate, key init, run
│   │   ├── main.go           #   run: 접속 → 유지보수 → 수집 → 판정 → 보고서 → 업로드 → 알림
│   │   └── key.go            #   key init: ed25519 키 생성. 이미 있으면 유지
│   └── gostodian-gate/       # 홈랩 게이트. SSH_ORIGINAL_COMMAND의 "동사 [이름]"만 받는다
│       └── main.go           #   허용 동사와 이름 검사, 고정 sudo 헬퍼 인자
├── internal/
│   ├── config/               # YAML 설정, GOSTODIAN_*_FILE 경로 덮어쓰기, 시크릿 파일 읽기
│   ├── ssh/                  # known_hosts로 호스트 키를 고정하는 SSH, 게이트 동사, 접속 실패 분류
│   ├── pipeline/             # docker_order 정지 순서와 기동 역순
│   ├── collect/              # 수집 동사, 로그 종류 고정 목록
│   ├── runlog/               # 실행 ID, 구조화 로그, 단계 이력, 수집 산출물, 시크릿 가림
│   ├── discord/              # 웹훅 알림. 원문 로그와 웹훅 주소는 제외
│   └── vpn/                  # VPN 인터페이스 확인
├── config/
│   └── config.yaml.example   # 설정 예시. config.yaml로 복사해서 사용. 실설정은 커밋하지 않는다
├── Doc/
│   ├── ROADMAP.md            # 범위, 용어, 확인이 필요한 항목, 운영 준비
│   └── Plan/                 # 상세 계획
├── Dockerfile                # distroless nonroot 이미지. gostodian만 포함
├── compose.yaml              # 1회 실행용 서비스. 시크릿과 이력 마운트
├── Makefile                  # build, test, lint
└── agent.md                  # 이 저장소에서 문서를 고칠 때의 말버릇

# git에 올리지 않는 로컬 디렉터리
secrets/    # SSH 키, known_hosts, 웹훅, API 키, MinIO 키
logs/       # 실행 이력 logs/<실행ID>.jsonl .json, 수집 logs/collects/<실행ID>/
bin/        # make build 산출물
LocalDoc/   # 개인 정리. 공유 문서가 아니다
```

실행이 끝나면 보고서는 로컬에 남고, `health` 성공 뒤 MinIO의 `reports/`, `logs/`, `collects/`로 올라간다. 업로드가 실패하면 로컬 파일이 그 실행의 산출물이다.
