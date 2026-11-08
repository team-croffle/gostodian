# gostodian

서버 점검 자동화 도구. 점검 머신에서 `gostodian run` 프로세스를 실행하면, 프로세스가 VPN·SSH로 대상 서버에 접속해 서비스 정지 → 패키지 갱신 → 재부팅 → 로그·리소스 수집 → AI 보고서 작성 → S3 업로드까지 수행. 시작과 결과는 Discord로 알림.

홈랩에 두는 헬퍼의 구현 계약은 [GOSTODIAN_SVC.md](GOSTODIAN_SVC.md) 참고.

## 설치

필요 환경: Go 1.26.10 이상. 컨테이너 실행시 Docker Compose v2.

```bash
make build                                   # bin/gostodian, bin/gostodian-gate
mkdir -p secrets logs
cp config/config.yaml.example config/config.yaml
cp secrets/secrets.example   secrets/secrets
```

`config/config.yaml`: 대상 서버 주소·점검 계정, `pipeline.docker_order`, 보고서 주소·모델, S3 주소·버킷 할당. 

[`secrets/secrets`](secrets/secrets.example) `AI_API_KEY`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `DISCORD_WEBHOOK`할당.



키와 known_hosts 준비:

```bash
./bin/gostodian config validate --config config/config.yaml
./bin/gostodian key init       --config config/config.yaml
ssh-keyscan -t ed25519 <대상호스트> >> secrets/known_hosts
```

`key init`이 출력한 공개키 한 줄을 대상 서버 점검 계정의 `authorized_keys`에 등록. 게이트·헬퍼 배치와 sudoers는 [GOSTODIAN_SVC.md](GOSTODIAN_SVC.md) 참고.

컨테이너 실행 시 시크릿은 값이 아니라 경로만 env로 전달 (`GOSTODIAN_CONFIG`, `GOSTODIAN_SSH_KEY_FILE`, `GOSTODIAN_KNOWN_HOSTS_FILE`, `GOSTODIAN_SECRETS_FILE`). 설정은 `compose.yaml`.

## 실행

```bash
# 로컬
./bin/gostodian run --config config/config.yaml

# 컨테이너
docker compose run --rm gostodian run --log-dir /var/lib/gostodian
```

빌드·테스트:

```bash
make build        # 바이너리 빌드
make docker       # 컨테이너 이미지 빌드
make test         # go test -race -shuffle=on ./...
make cover        # 커버리지 리포트
make lint         # 린트
make audit        # 보안 전체 점검 (CI와 동일)
```

사용 가능한 make 타깃은 `make help`로 확인.