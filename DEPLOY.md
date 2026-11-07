# 배포와 사용 (v1.0.0)

점검 머신에서 `gostodian run`을 한 번 실행하는 방법이다. 그 프로세스가 VPN과 SSH로 홈랩에 접속해 유지보수와 로그 수집을 하고, 보고서를 만들어 S3 호환 저장소에 올린다. 알림은 점검 머신이 Discord로 보낸다. 프로세스가 끝나면 `gostodian run`만 종료되고, 프로그램 파일은 점검 머신에 남는다.

홈랩에는 게이트 `gostodian-gate`와 헬퍼 `gostodian-svc`를 둔다. 헬퍼는 이 저장소에 없다. 별도 저장소에서 만들어 `/usr/local/libexec/gostodian-svc`에 둔다. 게이트는 허용된 동사만 그 헬퍼로 넘긴다.

`gostodian run`은 접속에 성공하면 패키지 갱신과 재부팅까지 이어서 한다. 실행 전에 점검 창을 잡아 둔다.

## 1. 점검 머신

### 1.1 빌드

Go 1.26.10 이상, GNU Make가 필요하다. Windows에서는 WSL 또는 Git Bash를 쓴다. 컨테이너로 실행할 때는 Docker Compose v2가 필요하다.

```bash
git clone <repo> && cd gostodian
make build
mkdir -p secrets logs
cp config/config.yaml.example config/config.yaml
```

`make build`는 `bin/gostodian`과 `bin/gostodian-gate`를 만든다. 점검 머신에는 `gostodian`만 두고, `gostodian-gate`는 홈랩으로 복사한다.

### 1.2 설정

`config/config.yaml`에 실제 값을 채운다. 실주소와 키 본문은 example에 넣지 않는다.

| 키 | 내용 |
|---|---|
| `server.host` / `port` / `user` | 홈랩 주소와 점검 계정 |
| `ssh.identity_file` / `known_hosts_file` | SSH 개인키, 홈랩 호스트 키 파일 |
| `pipeline.docker_order` | 정지 순서. 기동은 역순. 홈랩 허용 목록과 같은 이름만 적는다 |
| `pipeline.timeouts.step` | 게이트 호출 1회의 제한 시간. 기본 10분 |
| `pipeline.timeouts.reboot` | 재부팅 뒤 돌아올 때까지의 제한 시간. 기본 15분 |
| `pipeline.filter` | 같은 줄 합치기, 길이 상한. 상한을 비우면 262144글자 |
| `pipeline.redact` | 로그에 더 나오는 호스트명과 계정. IP, 토큰, 웹훅 주소는 설정 없이 가린다 |
| `report.api_url` / `model` | OpenAI 호환 Chat Completions base와 모델. 예시 값은 Claude 쪽이다. 제공자 이름은 코드에 없다 |
| `report.template_file` | 보고서 뼈대. 기본 `templates/report_template.md` |
| `report.timeout` | AI 호출 제한 시간. 기본 10분 |
| `report.max_input_runes` | AI에 넣는 수집 본문의 글자 상한. 기본 48000 |
| `report.max_output_tokens` | 응답 토큰 상한. 기본 8192 |
| `report.template_hash_file` | 직전 템플릿 해시. 기본 `logs/report_template.sha256` |
| `vpn.interface` | 비어 있으면 VPN 검사를 하지 않는다. 예: `wg0` |
| `secrets.*_file` | 시크릿 파일 경로. 값은 넣지 않는다 |

보고서 저장소는 S3 API다. MinIO, AWS S3, R2는 `s3.endpoint`, `s3.bucket`, `s3.region`과 시크릿의 `S3_ACCESS_KEY`, `S3_SECRET_KEY`만 바꾼다. Google Drive처럼 API가 다른 저장소는 이 설정으로 바뀌지 않는다. 업로드 키는 PutObject만 허용한다. 클라이언트 KMS 키는 쓰지 않는다. 실행 로그와 수집 본문은 올리지 않는다.

```bash
./bin/gostodian config validate --config config/config.yaml
```

### 1.3 시크릿

시크릿은 파일이다. 환경 변수에는 그 경로만 둔다. `secrets/`는 git에 올리지 않는다. 파일 권한은 `0600`으로 둔다.

| 파일 | 내용 |
|---|---|
| `secrets/id_ed25519` | 홈랩 접속용 SSH 개인키 |
| `secrets/known_hosts` | 홈랩 호스트 키. 검증을 끄지 않는다 |
| `secrets/secrets` | `AI_API_KEY`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `DISCORD_WEBHOOK` 한 줄씩 |

```bash
./bin/gostodian key init --config config/config.yaml
ssh-keyscan -t ed25519 <홈랩호스트> >> secrets/known_hosts
```

`key init`은 키 파일이 이미 있으면 바꾸지 않는다. 인증에 실패해도 키를 다시 만들지 않는다.

### 1.4 수동 실행

```bash
./bin/gostodian run --config config/config.yaml
```

다른 명령:

```bash
./bin/gostodian version
./bin/gostodian config validate --config config/config.yaml
./bin/gostodian key init --config config/config.yaml
```

### 1.5 스케줄

스케줄은 점검 머신에 상시 프로세스를 두지 않는다. systemd timer 또는 cron이 OS 시계에 맞춰 `gostodian run`을 한 번 실행한다. 시각과 유닛 파일은 코드가 아니라 점검 머신의 운영 설정이다. 스케줄 실행과 수동 실행은 같은 경로다.

```ini
# /etc/systemd/system/gostodian.service
[Service]
Type=oneshot
WorkingDirectory=/opt/gostodian
ExecStart=/opt/gostodian/bin/gostodian run --config /opt/gostodian/config/config.yaml
```

```ini
# /etc/systemd/system/gostodian.timer
[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
systemctl enable --now gostodian.timer
```

### 1.6 컨테이너

이미지는 `gostodian`만 담고, 셸이 없다. 사용자는 `65532`이고 루트 파일 시스템은 읽기 전용이다. 호스트 `logs/`는 그 사용자가 쓸 수 있어야 한다.

```bash
make docker
docker compose run --rm gostodian run --log-dir /var/lib/gostodian
```

이력은 컨테이너의 `/var/lib/gostodian`에 쓰이고, compose가 그 경로를 호스트 `logs/`에 연결한다. 시크릿은 `/run/secrets/`의 파일로 마운트되고, 환경 변수에는 그 경로만 있다. `config.yaml`의 시크릿 경로는 `GOSTODIAN_*_FILE`이 있으면 그 값이 이긴다.

## 2. 홈랩

점검 계정은 게이트만 실행할 수 있어야 한다. 점검 머신이나 컨테이너가 탈취되면 홈랩 접근으로 이어지므로, 아래만 연다.

1. 점검 계정의 `authorized_keys`에 한 줄만 둔다.

   ```
   restrict,from="<점검머신 VPN IP>",command="/usr/local/bin/gostodian-gate" ssh-ed25519 AAAA... gostodian
   ```

   `from=`이 점검 머신 VPN 주소가 아니면 sshd가 게이트를 시작하지 않는다. `command=` 때문에 실행되는 파일은 게이트뿐이다.

2. 방화벽은 VPN에서 22번만 허용한다. 점검 머신의 공개 주소에서 22번은 막는다.

3. `bin/gostodian-gate`를 `/usr/local/bin/gostodian-gate`에 둔다.

4. 별도 저장소의 `gostodian-svc`를 `/usr/local/libexec/gostodian-svc`에 둔다. 이 파일이 없으면 정지, 갱신, 재부팅, 로그 수집이 실행되지 않는다.

5. sudoers에는 헬퍼의 인자까지 고정된 줄만 등록한다. `docker`, `dnf *`, `systemd-run`은 등록하지 않는다.

   ```
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc down <서비스>
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc start <서비스>
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc update
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc reboot
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs dnf
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs boot
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs secure
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs fail2ban
   gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs audit
   ```

   `update-status`, `health`, `resources`는 sudo 없이 헬퍼를 부른다. `update`는 헬퍼 안에서 `dnf -y update`를 일회성 작업으로 돌린다. `collect-logs` 뒤의 `dnf`는 패키지 갱신 로그의 종류 이름이다.

6. 게이트의 서비스 이름 허용 목록과 `pipeline.docker_order`는 같은 이름만 쓴다. 허용 목록이 비어 있으면 `docker_order`도 비워 둔다. `down`과 `start`가 어느 compose 디렉터리를 가리키는지는 헬퍼에 둔다.

## 3. 보고서 저장소와 Discord

저장소 키는 PutObject만 연다. `gostodian run`은 `health`가 성공한 뒤에 보고서만 올린다. 키는 `reports/<실행ID>.md`다.

저장소가 안 떠 있거나 업로드가 실패하면 보고서는 점검 머신에 남고 Discord로 알린다. 실행 로그와 수집 본문은 항상 점검 머신에만 남는다. 이미 끝난 서비스 기동은 되돌리지 않는다.

Discord 웹훅 파일은 점검 머신에만 둔다. 알림 문장에 웹훅 주소, 키 본문, 원문 로그의 비밀은 넣지 않는다.

## 4. 한 번 실행되면

1. 설정과 시크릿 경로를 읽고, 실행 ID로 이력을 연다. Discord로 시작을 알린다.
2. `vpn.interface`가 있으면 그 인터페이스가 올라와 있는지 본다.
3. SSH로 `status`를 실행한다.
4. `docker_order` 순서로 서비스를 멈춘다.
5. `update`를 시작하고 `update-status`가 `done` 또는 `failed`가 될 때까지 본다.
6. `boot_id`를 저장하고 `reboot`를 호출한다. `timeouts.reboot` 안에 `boot_id`가 바뀌면 재접속으로 본다.
7. `dnf`, `boot`, `secure`, `fail2ban`, `audit`과 RAM, CPU, Storage를 읽는다. 한 종류가 실패해도 나머지 수집과 이후 기동은 이어간다.
8. 수집 본문에서 같은 줄을 합치고 가린다. 단어로 줄을 빼지 않는다. 이상 여부는 모델이 보고서에 적는다.
9. `docker_order`의 역순으로 서비스를 켜고 `health`를 본다.
10. `templates/report_template.md` 형식으로 보고서를 쓴다. 템플릿이 바뀌었거나 첫 실행이면 AI 요청에 템플릿 전문을 넣는다. 같으면 같은 형식으로 쓰라고만 한다. 모델은 로그를 본 뒤 종류별 판정을 보고서 끝에 적는다. AI가 실패하면 같은 템플릿에 실행 ID와 수집 요약만 채우고, 수집된 종류의 판정은 「판정 없음」이다. AI가 돌려준 글은 실행하지 않는다.
11. `health`가 성공했을 때만 보고서를 저장소에 올린다.
12. 완료 알림에 판정 요약과 보고서 위치를 넣는다. 게이트가 거부한 기록이 있으면 보고서에 남기고, 완료 알림과 따로 알린다.

수집이 아닌 단계가 실패하면 다음 단계로 가지 않고 실패를 알린다.

점검 머신에 남는 파일은 아래다.

| 경로 | 내용 |
|---|---|
| `logs/<실행ID>.jsonl` | 단계가 끝날 때마다 붙는 이력 |
| `logs/<실행ID>.json` | 끝난 뒤의 상태, 단계, 종류별 판정 |
| `logs/collects/<실행ID>/` | 종류별 수집 본문. `dnf`, `boot`, `secure`, `fail2ban`, `audit`, `resources` |
| `logs/reports/<실행ID>.md` | 보고서 |
| `logs/report_template.sha256` | 직전 보고서 템플릿의 해시 |

이력의 `host`와 `user`는 어느 대상을 점검했는지 남긴다. 수집 본문 안의 호스트명, IP, 계정, 토큰은 가린다.
