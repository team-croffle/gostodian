# gostodian

홈랩 서버의 정기 점검(유지보수)을 자동화하는 도구.

홈랩 **밖**의 점검 머신에서 `gostodian run` 프로세스를 한 번 실행하면, 그 프로세스가 VPN·SSH로 홈랩에 접속해 정해진 순서대로 유지보수를 수행하고, 결과를 실행 이력과 Discord로 남긴 뒤 종료된다. 홈랩에는 허용된 동사(작업 이름)만 통과시키는 게이트 `gostodian-gate` 하나만 둔다.

```
[점검 머신 / Docker]                                 [홈랩 서버]
  gostodian run                                       sshd
    │                                                  │ authorized_keys: restrict,from=,command=
    ├─ VPN 인터페이스 확인                               ▼
    ├─ SSH (ed25519, known_hosts 고정) ───────────▶  gostodian-gate   (허용 동사만 통과)
    │     status → stop… → update → reboot             │
    │     → boot_id 재확인 → start…(역순) → health       ▼
    │                                                 /usr/local/libexec/gostodian-svc  (sudo 고정 헬퍼)
    ├─ 실행 이력 (logs/<실행ID>.jsonl, .json)
    └─ Discord 웹훅 알림 (시작 · 완료 · 실패)
```

> **왜 점검 머신을 분리하나**
> 알림 주체가 점검 대상 위에 있으면 대상이 죽었을 때 알림도 같이 죽는다.
> 그래서 점검 결과 알림은 항상 점검 머신의 `gostodian run`이 직접 보낸다.

## 현재 상태

| 버전 | 범위 | 상태 |
| --- | --- | --- |
| v0.1.0 | 접속 — SSH 점검 1회, 접속 실패 분류, `key init` | 완료 |
| v0.2.0 | 게이트 — 허용 동사만 실행 | 완료 |
| v0.3.0 | 알림 — Discord 웹훅 | 완료 |
| v0.4.0 | 유지보수 — 정지 → `dnf` → 재부팅 → 재접속 → 역순 기동 → 헬스체크 | 구현 완료, 실서버 검증 전 |
| v0.5.0 | 수집·필터 — 로그·리소스 수집, 필터·리댁션·규칙 판정 | 계획 초안 |
| v1.0.0 | 보고서·업로드·스케줄 — AI 보고서, MinIO 업로드, 스케줄 실행 | 예정 |
| v1.1.0 | 감시 — 주기적 상태 확인, 상태 변화 알림 | 예정 |

자세한 범위는 [`Doc/ROADMAP.md`](Doc/ROADMAP.md), 버전별 상세 계획과 체크리스트는 [`Doc/Plan/`](Doc/Plan/)에 있다.

아직 없는 것: 로그 수집·리댁션(리댁션: 호스트명·IP·계정·토큰 가리기), AI 보고서, S3/MinIO 업로드, 스케줄 실행. AI·S3 시크릿 파일은 마운트 경로만 받고 아직 열지 않는다.

## `gostodian run`이 하는 일

1. 설정과 시크릿 파일 경로를 읽고, 실행 ID로 이력 파일을 연다.
2. Discord로 시작 알림을 보낸다.
3. `vpn.interface`가 설정돼 있으면 그 인터페이스가 올라와 있는지 확인한다. 없으면 `vpn_required`로 실패한다.
4. SSH로 접속해 게이트 `status`(hostname, uptime, whoami)를 실행한다. 접속 실패는 `auth_failed`, `host_key_mismatch`, `host_unreachable`로 분류해 이력과 알림에 남긴다.
5. 유지보수를 순서대로 진행한다. 각 게이트 호출에는 `timeouts.step`이 걸린다.
   1. `pipeline.docker_order` 순서로 서비스마다 `stop` (헬퍼에서는 `down`으로 실행해 재부팅 뒤 스스로 올라오지 않게 한다)
   2. `update` 시작 → `update-status`를 `done`/`failed`가 될 때까지 폴링
   3. 재부팅 전 `boot-id` 저장 → `reboot` → 지수 백오프로 재접속 → `boot_id`가 바뀌었는지 확인 (`timeouts.reboot` 안에)
   4. `docker_order` 역순으로 서비스마다 `start`
   5. `health` (컨테이너 기동 여부, 포트 Listening)
6. 중간 단계가 실패하면 다음 단계로 가지 않고 Discord로 실패를 알린다. 자동 롤백은 없다.
7. 성공·실패 이력 파일을 쓰고 `gostodian run` 프로세스가 종료된다.

> **주의** — 현재 `gostodian run`은 점검(status)에 성공하면 곧바로 `update`와 `reboot`까지 진행한다. 실서버에서 실행하기 전에 점검 창을 잡아 둘 것.

## 디렉터리 구조

```
gostodian/
├── cmd/
│   ├── gostodian/            # 점검 머신 CLI (version, config validate, key init, run)
│   │   ├── main.go           #   run: 접속 → 유지보수 파이프라인 → 이력 · 알림
│   │   └── key.go            #   key init: ed25519 키 생성 (있으면 유지)
│   └── gostodian-gate/       # 홈랩 게이트. SSH_ORIGINAL_COMMAND의 "동사 [이름]"만 받는다
│       └── main.go           #   허용 동사 · 허용 이름 검사, 고정 sudo 헬퍼 argv
├── internal/
│   ├── config/               # YAML 설정, GOSTODIAN_*_FILE 경로 덮어쓰기, 시크릿 파일 읽기
│   ├── ssh/                  # known_hosts 고정 SSH 클라이언트, 게이트 동사 실행, 실패 분류
│   ├── pipeline/             # docker_order 정지 순서 / 기동 역순
│   ├── runlog/               # 실행 ID, 구조화 로그(.jsonl), 단계 이력(.json), 시크릿 가림
│   ├── discord/              # 웹훅 알림 (원문 로그 · 웹훅 주소 제외)
│   └── vpn/                  # VPN 인터페이스 확인
├── config/
│   └── config.yaml.example   # 설정 예시. config.yaml로 복사해서 사용 (실설정은 커밋 금지)
├── Doc/
│   ├── ROADMAP.md            # 버전별 범위, 용어, 확인 필요 · 운영 준비 항목
│   └── Plan/                 # 버전별 상세 계획 (Template.md, v0.1.0 ~ v0.5.0)
├── .github/
│   ├── workflows/ci.yml      # lint · test · build
│   ├── workflows/security.yml# govulncheck · gosec · CodeQL · gitleaks · Trivy
│   └── dependabot.yml
├── Dockerfile                # distroless nonroot 이미지 (gostodian만 포함)
├── compose.yaml              # 하드닝된 1회 실행용 서비스, 시크릿 · 이력 마운트
├── Makefile                  # build · test · lint · audit 등
├── .golangci.yml             # 린트 규칙 (forbidigo 금지 API 포함)
├── lefthook.yml              # 로컬 커밋 · 푸시 훅
└── agent.md                  # AI 에이전트 작업 규칙

# git에 올리지 않는 로컬 디렉터리
secrets/    # SSH 키, known_hosts, 웹훅 등 시크릿 파일
logs/       # 실행 이력 (컨테이너에서는 /var/lib/gostodian)
bin/        # make tools / make build 산출물
LocalDoc/   # 개인 공부 · 정리 문서
```

## 요구사항

| | |
|---|---|
| Go | 1.26.10 이상 (`go.mod` 기준) |
| Docker | Compose v2 포함. gitleaks / Trivy도 컨테이너로 실행 |
| GNU Make | Windows에서는 WSL 또는 Git Bash |
| lefthook | 선택. 커밋 훅을 쓸 경우 |

## 시작하기

### 1. 도구 설치

```bash
git clone <repo> && cd gostodian
make tools   # bin/golangci-lint 설치
make hooks   # lefthook 커밋 훅 등록 (선택)
```

### 2. 설정

```bash
mkdir -p secrets logs
cp config/config.yaml.example config/config.yaml
```

| 키 | 내용 |
|---|---|
| `server.host` / `port` / `user` | 홈랩 주소와 점검 계정 |
| `ssh.identity_file` / `known_hosts_file` | SSH 개인키, 홈랩 호스트 키 파일 경로 |
| `pipeline.docker_order` | 정지 순서. 기동은 역순. 서비스 이름은 코드에 넣지 않고 여기에만 둔다 |
| `pipeline.timeouts.step` / `reboot` | 게이트 호출 1회 제한 시간 (기본 10m), 재부팅 복귀 제한 시간 (기본 15m) |
| `secrets.*_file` | 시크릿 파일 경로. compose의 `GOSTODIAN_*_FILE` env가 있으면 그쪽이 우선 |
| `vpn.interface` | 비어 있으면 VPN 검사를 하지 않는다. 예: `wg0` |

```bash
./bin/gostodian config validate --config config/config.yaml
```

### 3. 시크릿 준비

시크릿은 값이 아니라 **파일**로 관리한다. env에 값을 넣으면 `docker inspect`와 크래시 덤프에 그대로 남기 때문에, env에는 파일 경로만 둔다. `secrets/`는 `.gitignore` 처리되어 있다.

| 파일 | 내용 | 사용 시점 |
|---|---|---|
| `secrets/id_ed25519` | 홈랩 접속용 SSH 개인키 (권한 `0600`) | 지금 |
| `secrets/known_hosts` | 홈랩 호스트 키. **필수** — 검증을 끄지 않는다 | 지금 |
| `secrets/discord_webhook` | 알림용 Discord 웹훅 URL | 지금 |
| `secrets/ai_api_key` | 보고서 초안용 AI API 키 | v1.0.0 (지금은 빈 파일이어도 됨) |
| `secrets/s3_access_key`, `secrets/s3_secret_key` | 업로드용 키 | v1.0.0 (지금은 빈 파일이어도 됨) |

```bash
./bin/gostodian key init --config config/config.yaml      # 키가 없을 때만 생성. 공개키와 fingerprint 출력
ssh-keyscan -t ed25519 <홈랩호스트> >> secrets/known_hosts
```

`key init`은 키 파일이 이미 있으면 바꾸지 않는다. 인증 실패가 나도 키를 다시 만들지 않는다.

### 4. 홈랩 쪽 준비 (운영 준비, 이 저장소 밖)

이 컨테이너는 홈랩에 대한 SSH 권한을 쥔다. 즉 **컨테이너 탈취 = 홈랩 접근**이므로 홈랩 쪽 권한을 최소로 묶는다.

1. **`authorized_keys`** — 점검 계정에 아래 한 줄만 둔다.

   ```
   restrict,from="<점검머신 VPN IP>",command="/usr/local/bin/gostodian-gate" ssh-ed25519 AAAA... gostodian
   ```

   `from=`이 점검 머신 VPN 주소가 아니면 sshd가 게이트를 시작하지 않는다. `command=` 때문에 클라이언트가 보낸 문자열은 `SSH_ORIGINAL_COMMAND`로만 전달되고, 실행되는 파일은 게이트뿐이다.

2. **방화벽** — VPN에서 22번만 허용, 점검 머신의 퍼블릭 22번은 차단.

3. **게이트 설치** — `make build`로 만든 `bin/gostodian-gate`를 `/usr/local/bin/gostodian-gate`에 둔다.

4. **고정 헬퍼와 sudoers** — 게이트는 `docker`, `dnf`, `systemd-run`을 sudo로 직접 부르지 않는다. 대신 고정 헬퍼 `/usr/local/libexec/gostodian-svc`만 부르며, sudoers에는 그 헬퍼의 고정 인자 줄만 등록한다. 헬퍼 스크립트 자체는 저장소에 없고 운영 준비 항목이다.

   ```
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc down <서비스>
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc start <서비스>
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc update
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc reboot
   ```

   `update-status`, `health`는 읽기 전용이라 sudo 없이 헬퍼를 부른다. `update` 헬퍼는 내부에서 `systemd-run` 일회성 유닛으로 `dnf -y update`를 돌린다.

5. **서비스 이름 허용 목록** — 게이트는 허용 목록과 정확히 일치하는 이름만 `stop`/`start`에 통과시킨다. 이 목록은 아직 비어 있으므로(운영 준비), 채우기 전에는 `docker_order`를 비워 둬야 한다.

### 5. 실행

```bash
make build && ./bin/gostodian run --config config/config.yaml           # 로컬. 1회 수행 후 종료
docker compose run --rm gostodian run --log-dir /var/lib/gostodian      # 컨테이너. 이력은 호스트 logs/
```

컨테이너는 `read_only` 루트에 사용자 `65532`로 돈다. 호스트의 `logs/`는 그 사용자가 쓸 수 있어야 한다.

## 게이트 동사

게이트 입력은 `동사 [이름]` 한 줄이다. 목록 밖 동사, 허용 목록 밖 이름, 추가 인자는 실행하지 않고 거부한다.

| 동사 | 이름 | 하는 일 | 권한 |
|---|---|---|---|
| `status` | 없음 | hostname, uptime, whoami | 읽기 |
| `boot-id` | 없음 | `/proc/sys/kernel/random/boot_id` | 읽기 |
| `stop` | 필수 | 헬퍼 `down <이름>` | sudo 고정 줄 |
| `start` | 필수 | 헬퍼 `start <이름>` | sudo 고정 줄 |
| `update` | 없음 | 헬퍼 `update` | sudo 고정 줄 |
| `update-status` | 없음 | 헬퍼 `update-status` (`running`/`done`/`failed`) | 읽기 |
| `reboot` | 없음 | 헬퍼 `reboot` | sudo 고정 줄 |
| `health` | 없음 | 헬퍼 `health` | 읽기 |

## 개발

```bash
make build       # bin/gostodian, bin/gostodian-gate
make test        # go test -race -shuffle=on
make lint        # golangci-lint 전체
make lint-fix    # 자동 수정
make fmt         # gofumpt + goimports
make cover       # 커버리지 리포트 (coverage.html)
make audit       # lint + vuln + sec + secrets + scan-config (CI와 같은 점검)
make help        # 전체 타깃
```

버전 작업은 `Doc/Plan/vX.Y.Z.md`의 상세 구현 단위(3.1, 3.2, …)로 진행하고, 단위마다 완료 체크를 갱신한다. 작업 규칙은 [`agent.md`](agent.md)를 따른다.

## 품질 · 보안 게이트

로컬 훅(1차)과 CI(2차)를 의도적으로 겹쳐 둔다. 훅은 우회할 수 있고, CI는 우회할 수 없다.

| 도구 | 역할 | 로컬 | CI |
|---|---|---|---|
| golangci-lint | 린트 + 포맷. gosec 포함 | pre-commit, `make lint` | ✅ |
| gosec | Go SAST. MEDIUM 이상이면 실패 | `make sec` | ✅ SARIF |
| govulncheck | 실제 호출 경로 기준 취약점 | pre-push, `make vuln` | ✅ SARIF |
| CodeQL | taint 추적 | — | ✅ |
| gitleaks | 시크릿 | pre-commit | ✅ 히스토리 전체 |
| Trivy | Dockerfile / compose 오설정, 이미지 CVE | `make scan-*` | ✅ |
| Dependabot | gomod / actions / docker 주간 갱신 | — | ✅ |

예외 처리 주석 형식: golangci-lint는 `//nolint:<linter> // 이유`, 단독 gosec 게이트는 `// #nosec G<번호> -- 이유`를 읽는다. gosec 예외는 `#nosec` 형식이어야 CI 게이트에서 인정된다.

### 이 프로젝트에서 지키는 규칙

`forbidigo`로 강제되며 위반하면 린트가 실패한다.

- **`ssh.InsecureIgnoreHostKey` 금지.** `knownhosts.New()`를 쓴다. 홈랩을 재설치했다면 `secrets/known_hosts`를 갱신하는 게 맞는 대응이다.
- **`InsecureSkipVerify` 금지.** 내부 서비스는 CA를 `RootCAs`에 추가한다.
- **시크릿은 `os.Getenv`가 아니라 파일에서 읽는다.** 일반 설정값이면 `//nolint:forbidigo`로 예외 처리한다.

코드로 강제되지 않지만 똑같이 중요한 것:

- **원격 명령은 고정 목록으로만 실행한다.** 셸 문자열로 조립하지 않는다 (gosec G204).
- **점검 실패에 자동 롤백은 없다.** 멈추고 알린다.
- **로그는 신뢰할 수 없는 입력이다** (v0.5.0~). fail2ban에 찍히는 실패 계정명은 공격자가 제어하는 문자열이다. AI API로 보내기 전에 반드시 리댁션하고, 구조화된 필드에 넣어 경계를 명시한다.
- **LLM 출력으로 아무것도 실행하지 않는다** (v1.0.0~). 판정은 규칙 기반, LLM은 코멘터리 전용. AI 호출이 실패해도 보고서는 나가야 한다.

## 라이선스

미정.
