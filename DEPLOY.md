# 배포 가이드

동작 전체 설명은 [README.md](README.md), 홈랩 헬퍼 계약은 [GOSTODIAN_SVC.md](GOSTODIAN_SVC.md) 참고.

용어는 둘로 고정.

- **점검 머신**: `gostodian run`을 실행하는 쪽. 대상 서버 밖에 둠.
- **홈랩(대상 서버)**: 점검을 받는 쪽. 여기에 게이트와 헬퍼를 둠.

## 1. 배포 전 필요한 것

**홈랩(대상 서버)**

- SSH 서버(`sshd`)와 `sudo`
- systemd (`systemctl`, `systemd-run`) — `reboot`, `update`에서 사용
- `dnf` 계열 배포판 — 패키지 갱신이 `dnf -y update`
- Docker + Compose v2 — 점검이 끄고 켜는 서비스 운용
- `ss` (iproute2) — `health`의 포트 확인
- 점검용 로그 파일 접근 권한(`secure`, `audit` 등은 root)

**점검 머신**

- 홈랩에 닿는 네트워크. VPN을 쓰면 그 인터페이스가 올라와 있어야 함
- `gostodian` 바이너리를 직접 실행하거나, 컨테이너로 실행할 Docker + Compose v2
- (빌드까지 할 경우) Go 1.26.10 이상

> 빌드는 점검 머신이 아닌 곳에서 해도 됨. 산출물 바이너리만 각 자리로 옮기면 됨.

## 2. 바이너리 빌드

점검 머신용 `gostodian`과 홈랩용 `gostodian-gate`를 함께 만듦.

```bash
make build        # bin/gostodian, bin/gostodian-gate
```

`gostodian`은 점검 머신에, `gostodian-gate`는 홈랩에 둔다. 단, 게이트는 아래 4단계에서 서비스 이름을 채운 뒤 다시 빌드해야 한다.

## 3. svc 헬퍼 제작·업로드

헬퍼 코드는 이 저장소에 없다. [GOSTODIAN_SVC.md](GOSTODIAN_SVC.md)의 계약대로 별도로 만든다. 만들 때 홈랩 값으로 채우는 것:

- 서비스 이름 ↔ compose 프로젝트·디렉터리·파일 표
- `health`가 볼 컨테이너와 포트
- `collect-logs` 종류별 로그 경로

1. 헬퍼를 홈랩 `/usr/local/libexec/gostodian-svc`에 업로드.
2. 소유와 권한 설정.

```bash
sudo chown root:root /usr/local/libexec/gostodian-svc
sudo chmod 0755 /usr/local/libexec/gostodian-svc
```

`0755`인 이유: 점검 계정은 실행만 하고 수정은 못 함. 쓰기 권한이 있으면 sudo 고정 줄이 그 계정의 스크립트가 됨.

## 4. 게이트 업로드

게이트는 서비스 이름 허용 목록을 소스에 담는다. 현재 `cmd/gostodian-gate/main.go`의 `allowedNames`는 비어 있다.

1. `allowedNames`에 홈랩 서비스 이름을 채운다. 이 목록은 점검 머신 `pipeline.docker_order`, svc 헬퍼의 이름 표와 같아야 한다.
2. 다시 빌드한다(`make build`).
3. `bin/gostodian-gate`를 홈랩에 업로드하고 권한을 준다.

```bash
sudo install -o root -g root -m 0755 gostodian-gate /usr/local/bin/gostodian-gate
```

## 5. 점검용 계정 생성 (홈랩)

점검 전용 계정을 하나 만든다. 아래는 계정 이름이 `gostodian`일 때의 예.

```bash
sudo useradd -m -s /bin/bash gostodian
sudo usermod -aG docker gostodian   # health가 sudo 없이 도커 소켓을 읽어야 함
```

`docker` 그룹은 `health`가 sudo 없이 컨테이너 상태를 보기 위한 것.

## 6. sudoers 등록 (홈랩)

root가 필요한 동사(`down`, `start`, `update`, `reboot`, `collect-logs`)만 NOPASSWD로 연다. 명령은 헬퍼 경로 한 줄씩, 인자까지 고정한다. `update-status`, `health`, `resources`는 sudo 없이 돌아가므로 넣지 않는다.

서비스가 `minio` 하나일 때의 예:

```
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc down minio
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc start minio
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc update
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc reboot
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs dnf
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs boot
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs secure
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs fail2ban
gostodian ALL=(root) NOPASSWD: /usr/local/libexec/gostodian-svc collect-logs audit
```

`sudo -n`은 비밀번호를 묻지 않는다. sudoers와 인자가 한 글자라도 다르면 그 단계는 실패한다. 더 자세한 표는 [GOSTODIAN_SVC.md](GOSTODIAN_SVC.md) 참고.

## 7. 키 준비 (점검 머신 → 홈랩)

1. 점검 머신에서 설정과 키를 준비.

```bash
cp config/config.yaml.example config/config.yaml   # 값은 9단계에서 채움
./bin/gostodian key init --config config/config.yaml
```

`key init`은 `ssh.identity_file`에 개인키가 없으면 ed25519 개인키를 만들고 공개키 한 줄을 화면에 낸다. 키가 이미 있으면 그대로 두고 지문만 낸다. 개인키 본문은 화면에 나오지 않는다.

2. 그 공개키 한 줄을 홈랩 점검 계정의 `~/.ssh/authorized_keys`에 넣는다. 이때 강제 명령으로 게이트만 돌게 묶는다.

```
restrict,from="<점검머신 VPN IP>",command="/usr/local/bin/gostodian-gate" ssh-ed25519 AAAA... gostodian
```

이렇게 묶으면 점검 계정으로 접속해도 임의 명령이 아니라 게이트만 실행된다. 게이트는 허용된 동사만 통과시킨다.

## 8. known_hosts 등록 (점검 머신)

홈랩 호스트 키를 미리 받아 둔다. 접속 시 호스트 키 불일치면 점검이 실패하게 되어 있다.

```bash
ssh-keyscan -t ed25519 <홈랩호스트> >> secrets/known_hosts
```

## 9. 설정·시크릿 채우기 (점검 머신)

```bash
cp secrets/secrets.example secrets/secrets
```

- `config/config.yaml`: 홈랩 주소·점검 계정, `pipeline.docker_order`(4·5단계 이름과 동일), 보고서 주소·모델, S3 주소·버킷. VPN을 쓰면 `vpn.interface`도.
- `secrets/secrets`: `AI_API_KEY`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `DISCORD_WEBHOOK`.

설정 검증:

```bash
./bin/gostodian config validate --config config/config.yaml
```

## 10. 실행

```bash
# 로컬
./bin/gostodian run --config config/config.yaml

# 컨테이너
docker compose run --rm gostodian run --log-dir /var/lib/gostodian
```

점검 시작과 결과는 Discord로 온다. 실행 로그와 수집 본문은 점검 머신에 남고, `health`가 성공했을 때만 보고서가 S3에 올라간다.

## 점검 순서 체크리스트

- [ ] 홈랩: Docker, systemd, dnf, ss, sshd/sudo 확인 
- [ ] 바이너리 빌드 
- [ ] svc 헬퍼 제작·업로드, `root:root`·`0755` 
- [ ] `allowedNames` 채우고 게이트 재빌드·업로드 
- [ ] 점검 계정 생성, `docker` 그룹 
- [ ] sudoers 등록 
- [ ] `key init`, authorized_keys 강제 명령 등록 
- [ ] known_hosts 등록 
- [ ] config·secrets 채우고 `config validate` 
- [ ] `gostodian run` 
