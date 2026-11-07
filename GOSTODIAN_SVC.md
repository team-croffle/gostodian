# gostodian-svc

홈랩에 두는 헬퍼의 구현 계약이다. 이 파일의 코드는 이 저장소에 없다. 별도 저장소에서 만들어 `/usr/local/libexec/gostodian-svc`에 둔다. `gostodian-gate`는 그 경로만 호출한다.

헬퍼는 상주 프로세스가 아니다. sshd가 SSH 세션마다 게이트를 시작하고, 게이트가 허용된 동사일 때만 헬퍼를 실행한다. 그 동사가 끝나면 헬퍼와 게이트가 함께 종료된다.

`status`와 `boot-id`는 헬퍼가 아니다. 게이트가 `hostname`, `uptime`, `whoami`와 `/proc/sys/kernel/random/boot_id`를 직접 읽는다.

## 1. 배치

| 항목 | 값 |
|---|---|
| 경로 | `/usr/local/libexec/gostodian-svc` |
| 소유 | `root:root` |
| 권한 | `0755`. 점검 계정은 실행만 한다. 파일을 고칠 수 있으면 sudo 고정 줄이 그 계정의 스크립트가 된다 |
| 형태 | 인자로 분기하는 실행 파일 하나. 셸 스크립트여도 된다 |
| 설정 | 서비스 이름과 compose 경로, health가 볼 포트, 로그 파일 묶음은 헬퍼 안의 표. 이 저장소와 점검 머신 YAML에는 두지 않는다 |

호출은 셸 문자열로 붙지 않는다. 게이트가 넘기는 인자 목록은 아래가 전부다.

| 게이트가 받은 동사 | 헬퍼 argv | 권한 |
|---|---|---|
| `stop <서비스>` | `sudo -n /usr/local/libexec/gostodian-svc down <서비스>` | root |
| `start <서비스>` | `sudo -n /usr/local/libexec/gostodian-svc start <서비스>` | root |
| `update` | `sudo -n /usr/local/libexec/gostodian-svc update` | root |
| `reboot` | `sudo -n /usr/local/libexec/gostodian-svc reboot` | root |
| `collect-logs <종류>` | `sudo -n /usr/local/libexec/gostodian-svc collect-logs <종류>` | root |
| `update-status` | `/usr/local/libexec/gostodian-svc update-status` | 점검 계정 |
| `health` | `/usr/local/libexec/gostodian-svc health` | 점검 계정 |
| `resources` | `/usr/local/libexec/gostodian-svc resources` | 점검 계정 |

`<종류>`는 `dnf`, `boot`, `secure`, `fail2ban` 중 하나다. 여기서 `dnf`는 패키지 갱신 로그의 종류 이름이다. `dnf` 바이너리를 실행하는 인자가 아니다. sudoers에는 종류마다 한 줄씩, 서비스마다 `down` 한 줄과 `start` 한 줄을 올린다. `docker`, `dnf *`, `systemd-run`은 sudoers에 없다. 그 명령은 root로 돈 헬퍼 안에서만 부른다.

`sudo -n`은 비밀번호를 묻지 않는다. sudoers와 인자가 한 글자라도 다르면 sudo가 실패하고, 그 단계는 점검 머신에서 실패다.

## 2. 게이트가 출력을 넘기는 방식

게이트는 `exec`로 헬퍼를 실행한다.

- 종료 코드가 0이면 헬퍼 stdout만 점검 머신으로 간다. 점검 머신은 그 본문 전체를 저장한다. 시간 범위, 파일 이름 목록, 바이트 상한을 게이트가 자르지 않는다.
- 종료 코드가 0이 아니면 게이트는 stdout을 버리고, 자신의 stderr에 `동사: exit status N`만 남긴 뒤 1로 끝난다. 헬퍼 stderr도 게이트가 버린다.
- 그래서 실패 이유를 점검 이력에 남기려면 종료 코드 0과 stdout 첫 단어로 전달해야 한다. `update-status`가 이 방식이다. `health`와 `down`, `start`, `collect-logs`는 실패를 0이 아닌 종료 코드로 알리므로, 이유 문장은 점검 머신에 닿지 않는다. 이유는 헬퍼가 홈랩 로컬에 남긴다.

한 단계의 제한 시간은 점검 머신 `timeouts.step`이고 기본은 10분이다. 이 안에 헬퍼가 돌아오지 않으면 그 호출은 실패다.

## 3. 공통 규칙

- 알 수 없는 첫 인자, 인자가 더 있는 경우, 이름에 공백이 있는 경우는 종료 코드 1이다. 아무것도 실행하지 않는다.
- 서비스 이름은 게이트 허용 목록, 점검 머신 `pipeline.docker_order`, 헬퍼의 이름 표가 같다. 표에 없는 이름은 `down`과 `start`를 하지 않는다.
- 로그 파일을 지우지 않는다. 홈랩이 자기 주기로 로그를 지운다. 헬퍼는 그 시점에 있는 파일을 읽기만 한다.
- stdout에는 비밀 원문을 일부러 넣지 않을 수 없다. 로그 본문 자체가 수집 대상이다. 가림은 점검 머신이 받는 쪽에서 한다. 헬퍼는 상태 파일과 로컬 진단 로그에 웹훅, API 키, 개인키를 쓰지 않는다.

## 4. `down <서비스>`

재부팅 뒤에 그 서비스가 스스로 올라오지 않게 끈다. `compose stop`만 하면 재시작 정책으로 다시 뜰 수 있으므로 `compose down`으로 끈다.

1. 이름 표에서 그 서비스의 compose 프로젝트와 작업 디렉터리, 서비스 이름을 찾는다.
2. 그 서비스만 내린다. 표에 있는 다른 서비스를 같이 내리지 않는다. 점검 머신이 `docker_order` 순서대로 한 서비스씩 호출한다.
3. 이미 내려가 있으면 종료 코드 0이다.
4. 내려간 뒤에 종료 코드 0. 내리지 못하면 종료 코드 1.

compose 디렉터리는 홈랩 운용자가 표에 적는다. 이 문서에 디렉터리를 확정하지 않는다.

## 5. `start <서비스>`

`down`과 같은 이름 표로 그 서비스만 올린다. 점검 머신이 `docker_order`의 역순으로 한 서비스씩 호출한다.

1. 표에 있는 그 서비스만 `compose up -d`로 올린다.
2. 이미 떠 있으면 종료 코드 0이다.
3. 뜨지 않으면 종료 코드 1. 다음 서비스 기동은 점검 머신이 하지 않는다.

`health`는 `start` 전부가 끝난 뒤에 점검 머신이 따로 호출한다. `start` 안에서 health를 대신하지 않는다.

## 6. `update`

패키지 갱신을 시작하고 바로 돌아온다. SSH 세션 안에서 `dnf -y update`가 끝날 때까지 기다리지 않는다. 갱신이 10분을 넘기면, 기다리는 쪽은 아래 `update-status` 폴링이다. 그 폴링도 `timeouts.step`(기본 10분)을 넘기면 점검 머신이 `update-status: timeout`으로 중단한다. 홈랩 갱신이 그보다 길면 점검 머신 설정의 `timeouts.step`을 늘린다.

상태 파일은 `/run/gostodian/update-status`다. `/run`은 재부팅이면 사라진다. 갱신은 재부팅 전에 끝나야 하므로 이 경로가 맞다. 파일에는 단어 하나만 쓴다.

1. 이미 `gostodian-update` 유닛이 돌고 있으면 파일을 바꾸지 않고 종료 코드 0으로 돌아온다.
2. 디렉터리 `/run/gostodian`을 만들고 파일에 `running`을 쓴 뒤에 돌아온다. 이 쓰기 전에 종료하면 점검 머신이 빈 상태를 실패로 본다.
3. `systemd-run`으로 일회성 유닛 `gostodian-update`를 만든다. `--wait`는 붙이지 않는다.
4. 유닛이 실행하는 명령은 `dnf -y update` 하나다.
5. 그 명령의 종료 코드가 0이면 유닛이 파일을 `done`으로 덮어쓴다. 0이 아니면 `failed`로 덮어쓴다. `done`은 `dnf`가 끝난 뒤에만 쓴다. 도중에 `done`이면 재부팅이 갱신을 끊는다.
6. `systemd-run` 자체에 실패하면 파일을 `failed`로 쓰고 종료 코드 1이다. 유닛을 만드는 데 성공하면 종료 코드 0이다.

sudoers에는 `systemd-run`과 `dnf`가 없고, 이 헬퍼의 `update` 한 줄만 있다.

## 7. `update-status`

상태 파일을 읽어 stdout에 쓰고 종료 코드는 항상 0이다. 점검 머신은 종료 코드가 0일 때만 첫 단어를 읽는다. 첫 단어는 대소문자를 가리지 않는다. 그 뒤의 단어는 무시한다. 폴링 간격은 2초다.

| 파일 내용 | stdout | 점검 머신 |
|---|---|---|
| `running` | `running` | 2초 뒤 다시 호출 |
| `done` | `done` | 갱신 단계를 끝내고 재부팅으로 진행 |
| `failed` | `failed` | `update-status: failed`로 중단. 재부팅하지 않음 |
| 파일 없음, 빈 파일, 다른 단어 | `failed` | 같은 중단 |

이 동사를 0이 아닌 코드로 끝내면 점검 머신은 단어를 보지 않고 게이트 종료 코드로 실패 처리한다. 상태 의미는 stdout으로만 전달한다.

## 8. `reboot`

호스트를 재부팅한다. 재부팅 전의 `boot_id` 저장과, 재부팅 뒤 값이 바뀌었는지 확인하는 일은 게이트와 점검 머신이 한다. 헬퍼는 재부팅만 시작한다.

1. `systemctl reboot`로 재부팅을 건다.
2. 명령이 0으로 돌아오면 헬퍼도 0으로 끝난다. 재부팅이 세션을 먼저 끊으면 점검 머신은 그 끊김을 이 단계의 성공으로 보고, `boot_id`가 바뀔 때까지 다시 접속한다.
3. 재부팅을 걸지 못한 채 0이 아닌 코드로 끝나면 점검 머신은 재접속하지 않고 실패로 중단한다.

## 9. `collect-logs <종류>`

한 호출은 한 종류다. 그 종류의 파일을 순서대로 이어 붙여 stdout으로 내보내고 종료 코드 0이다. 점검 머신은 이 stdout을 `logs/collects/<실행ID>/<종류>`에 저장한 뒤 거르고 가린다.

종류와 홈랩에서 보통 읽는 경로다. 실제 파일 이름은 홈랩에 맞게 표로 확정한다.

| 종류 | 읽는 대상 |
|---|---|
| `dnf` | `/var/log/dnf.log`와 그 회전본. 패키지 갱신 로그 |
| `boot` | `/var/log/boot.log`와 `boot.log-날짜` 회전본 |
| `secure` | `/var/log/secure`와 `secure-날짜` 회전본 |
| `fail2ban` | `/var/log/fail2ban.log`와 `fail2ban.log-날짜` 회전본 |

회전본 파일 이름의 날짜는 그 파일이 닫힌 날이다. 내용의 첫 시각은 그보다 앞일 수 있다. 헬퍼는 날짜로 줄을 잘라내지 않고, 있는 파일 전체를 읽는다.

순서:

1. 그 종류의 회전본을 이름 오름차순으로 출력한다.
2. 이어서 현재 파일을 출력한다.
3. 파일 앞에 한 줄을 붙인다. `# <절대경로>`. 본문은 파일 내용 그대로다.
4. 표에 있는 파일이 하나도 없으면 종료 코드 1이다. 그러면 점검 머신은 그 종류를 「수집 없음」으로 둔다. 빈 성공(종료 코드 0, 본문 없음)은 「이상 없음」이 되므로 파일이 없을 때 0으로 끝내면 안 된다.
5. 일부 회전본만 없어도, 하나라도 읽었으면 읽은 것을 출력하고 종료 코드 0이다.
6. 읽기 권한이 없거나 도중 오류면 종료 코드 1이다.

이 동사는 sudo로 root가 된다. `secure`는 보통 점검 계정이 직접 읽지 못한다.

## 10. `resources`

RAM, CPU, Storage를 stdout에 쓰고 종료 코드 0이다. sudo 없이 점검 계정이 읽을 수 있는 값만 쓴다. 점검 머신은 필드를 해석하지 않고 본문을 저장한다. 제목 세 줄은 판정 규칙이 종류를 알아볼 수 있게 고정한다.

```
RAM
MemTotal: <kB>
MemAvailable: <kB>
CPU
loadavg: <1분> <5분> <15분>
nproc: <개수>
Storage
<파일시스템> <크기> <사용> <남음> <사용률> <마운트>
```

`RAM`은 `/proc/meminfo`의 `MemTotal`, `MemAvailable`이다. `CPU`는 `/proc/loadavg`의 앞 세 칸과 `nproc`다. `Storage`는 `df -hP`의 데이터 줄이다. 하나를 읽지 못하면 종료 코드 1이다.

## 11. `health`

컨테이너가 떠 있는지, 지정한 포트가 Listening인지 본다. sudo 없이 실행되므로 점검 계정은 도커 소켓을 읽을 수 있어야 한다. 계정에 `docker` 그룹을 주는 일은 홈랩 운영 준비다.

볼 컨테이너와 포트는 헬퍼 안의 표다. 서비스 이름은 코드 저장소에 적지 않는다. `start`로 올린 집합과 이 표가 같아야 한다.

1. 표의 컨테이너가 모두 running인지 본다.
2. 표의 포트가 모두 Listening인지 본다. `ss -ltn`으로 확인한다.
3. 모두 맞으면 stdout에 확인한 컨테이너와 포트를 한 줄씩 쓰고 종료 코드 0이다.
4. 하나라도 아니면 종료 코드 1이다. 이 이유는 게이트가 점검 머신으로 넘기지 않으므로, 헬퍼가 `/var/log/gostodian-svc.log`에 어떤 컨테이너 또는 포트가 실패했는지 남긴다.

`health`가 0이 아니면 점검 머신은 보고서를 MinIO에 올리지 않는다. 이미 끝난 기동은 되돌리지 않는다.

## 12. 인자 분기

실행 파일의 분기는 이 형태다. compose 경로와 포트, 로그 경로는 홈랩에서 표를 채운 뒤에야 동작한다.

```sh
#!/bin/sh
set -eu
name=${2-}
case "${1-}" in
  down)
    [ -n "$name" ] || exit 1
    # 이름 표의 그 서비스만 compose down
    ;;
  start)
    [ -n "$name" ] || exit 1
    # 이름 표의 그 서비스만 compose up -d
    ;;
  update)
    [ -z "$name" ] || exit 1
    # /run/gostodian/update-status 에 running 을 쓰고
    # systemd-run --unit=gostodian-update 으로 dnf -y update
    ;;
  update-status)
    [ -z "$name" ] || exit 1
    # 파일의 단어 또는 failed 를 stdout 에 쓰고 exit 0
    ;;
  reboot)
    [ -z "$name" ] || exit 1
    exec systemctl reboot
    ;;
  collect-logs)
    case "$name" in
      dnf|boot|secure|fail2ban) ;;
      *) exit 1 ;;
    esac
    # 그 종류 파일을 오래된 순으로 stdout 에 이어 붙인다
    ;;
  health)
    [ -z "$name" ] || exit 1
    # 컨테이너와 포트. 실패하면 로컬 로그 후 exit 1
    ;;
  resources)
    [ -z "$name" ] || exit 1
    # RAM, CPU, Storage
    ;;
  *)
    exit 1
    ;;
esac
```

## 13. 점검 머신과의 순서

헬퍼를 직접 모아 보면 호출 순서는 아래다. 한 줄이 헬퍼 실행 한 번이다.

1. `down <서비스>`를 `docker_order` 순서대로
2. `update`
3. `update-status`를 `done` 또는 `failed`가 나올 때까지
4. `reboot`
5. 재접속은 게이트의 `boot-id`. 헬퍼를 부르지 않는다
6. `collect-logs dnf`, `collect-logs boot`, `collect-logs secure`, `collect-logs fail2ban`, `resources`
7. `start <서비스>`를 역순으로
8. `health`

`collect-logs`와 `resources`가 실패해도 점검 머신은 나머지 수집과 `start`, `health`를 계속한다. `down`, `update`, `update-status`, `reboot`, `start`, `health`가 실패하면 다음으로 가지 않는다.
