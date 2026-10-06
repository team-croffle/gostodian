# gostodian

홈랩 서버의 정기 점검을 자동화하는 도구.
별도의 **점검 머신**에서 컨테이너로 돌면서 홈랩 서버에 SSH로 접속해 점검을 수행하고,
로그를 수집·분석한 뒤 AI API로 보고서 초안을 만들어 S3에 올리고 Discord로 알린다.

```
[점검 머신 / Docker]                         [홈랩 서버]
  gostodian ──SSH(ed25519, known_hosts 고정)──▶ 서비스 정지 → dnf update → 재부팅
      │                                          → 서비스 기동 → 로그 수집
      ├─ 로그 정규화 · 이상 탐지 (규칙 기반)
      ├─ 리댁션 (호스트명 / IP / 계정 / 토큰 마스킹)
      ├─ AI API → 보고서 코멘터리 초안
      ├─ S3 업로드 (SSE, PutObject 전용 키)
      └─ Discord 웹훅 알림
```

> **왜 점검 머신을 분리하나**
> 알림 주체가 점검 대상 위에 있으면 대상이 죽었을 때 알림도 같이 죽는다.
> 홈랩에서 돌리는 메인 Discord 봇과 별개로, 점검 결과 알림은 반드시 이 프로세스가 보낸다.

## 상태

초기 개발 단계. 현재는 프로젝트 기반(린트 / 보안 스캔 / CI / 컨테이너)만 구성되어 있다.

## 요구사항

| | |
|---|---|
| Go | 1.26 이상 (`go.mod` 기준) |
| Docker | Compose v2 포함. gitleaks / Trivy를 컨테이너로 실행 |
| GNU Make | Windows에서는 WSL 또는 Git Bash |
| lefthook | 선택. 커밋 훅을 쓸 경우 |

## 시작하기

### 1. 도구 설치

```bash
git clone <repo> && cd gostodian
make tools   # bin/golangci-lint 설치
make hooks   # lefthook 커밋 훅 등록 (선택)
```

### 2. 시크릿 준비

`secrets/` 디렉토리는 `.gitignore` 처리되어 있다. 값이 아니라 **파일**로 관리한다 —
env로 넣으면 `docker inspect`와 크래시 덤프에 그대로 남는다.

```bash
mkdir -p secrets config
```

| 파일 | 내용 |
|---|---|
| `secrets/id_ed25519` | 홈랩 접속용 SSH 개인키 (권한 `0600`) |
| `secrets/known_hosts` | 홈랩 호스트 키. **필수** — 검증을 끄지 않는다 |
| `secrets/ai_api_key` | 보고서 초안 생성용 AI API 키 |
| `secrets/s3_access_key` | S3 액세스 키 |
| `secrets/s3_secret_key` | S3 시크릿 키 |
| `secrets/discord_webhook` | 알림용 Discord 웹훅 URL |

호스트 키는 직접 채운다:

```bash
ssh-keyscan -t ed25519 <홈랩호스트> >> secrets/known_hosts
```

### 3. 홈랩 서버 쪽 준비

이 컨테이너는 홈랩에 대한 SSH 권한을 쥔다. 즉 **컨테이너 탈취 = 홈랩 장악**이다.
폭발 반경을 줄이기 위해 홈랩의 `~/.ssh/authorized_keys`에 실행 제약을 건다:

```
restrict,command="/usr/local/bin/gostodian-agent",from="<점검머신IP>" ssh-ed25519 AAAA... gostodian
```

이렇게 하면 키가 유출돼도 정해진 점검 스크립트 외에는 실행할 수 없다.

### 4. 설정

`config/config.yaml`에 점검 대상 호스트, 서비스 정지/기동 순서(service → network → DB),
수집할 로그(dnf, boot, security, fail2ban), S3 버킷/프리픽스를 기술한다.

### 5. 실행

```bash
make build && ./bin/gostodian    # 로컬
docker compose up -d             # 컨테이너 (권장)
docker compose logs -f
```

## 개발

```bash
make lint        # golangci-lint 전체
make lint-fix    # 자동 수정
make fmt         # gofumpt + goimports
make test        # go test -race -shuffle=on
make cover       # 커버리지 리포트 (coverage.html)
make audit       # 보안 전체 점검 — CI와 동일
make help        # 전체 타깃
```

## 품질 · 보안 게이트

로컬 훅(1차)과 CI(2차)를 의도적으로 겹쳐 둔다. 훅은 우회 가능하고, CI는 우회 불가능하다.

| 도구 | 역할 | 로컬 | CI |
|---|---|---|---|
| golangci-lint | 린트 + 포맷. gosec 포함 | ✅ | ✅ |
| gosec | Go SAST | `make sec` | ✅ SARIF |
| govulncheck | 실제 호출 경로 기준 취약 의존성 | pre-push | ✅ SARIF |
| CodeQL | taint 추적 (로그 → AI API / 셸) | — | ✅ |
| gitleaks | 시크릿 | pre-commit | ✅ 히스토리 전체 |
| Trivy | Dockerfile / compose 오설정, 이미지 CVE | `make scan-*` | ✅ |
| Dependabot | gomod / actions / docker 주간 갱신 | — | ✅ |

### 이 프로젝트에서 지키는 규칙

`forbidigo`로 강제되며 위반 시 린트가 실패한다.

- **`ssh.InsecureIgnoreHostKey` 금지.** `knownhosts.New()`를 쓴다. 홈랩을 재설치했다면
  `secrets/known_hosts`를 갱신하는 게 맞는 대응이다.
- **`InsecureSkipVerify` 금지.** 내부 서비스는 CA를 `RootCAs`에 추가한다.
- **시크릿은 `os.Getenv`가 아니라 `/run/secrets` 파일에서 읽는다.**
  일반 설정값이면 `//nolint:forbidigo`로 예외 처리한다.

코드로 강제되지 않지만 동일하게 중요한 것:

- **원격 명령은 화이트리스트 테이블로 고정한다.** 문자열로 조립하지 않는다 (gosec G204).
- **로그를 AI API로 보내기 전 반드시 리댁션한다.** journald/fail2ban 로그에는 내부 IP,
  호스트명, 계정명이 그대로 들어있다. 리댁션 레이어에는 골든 테스트를 붙인다.
- **로그는 신뢰할 수 없는 입력이다.** fail2ban에 찍히는 실패 계정명은 공격자가 제어하는
  문자열이고, 그게 그대로 프롬프트에 들어간다. 구조화된 필드에 넣고 경계를 명시한다.
- **LLM 출력으로 아무것도 실행하지 않는다.** 판정은 규칙 기반, LLM은 코멘터리 전용.
  AI API 호출이 실패해도 보고서는 나가야 한다.

## 라이선스

미정.
