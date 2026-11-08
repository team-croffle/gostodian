# gostodian-svc

홈랩에 두는 헬퍼의 구현 계약이다. 이 저장소에는 코드가 없다. 별도 저장소에서 만들어 `/usr/local/libexec/gostodian-svc`에 둔다. `gostodian-gate`는 그 경로만 호출한다.

헬퍼는 상주 프로세스가 아니다. sshd가 SSH 세션마다 게이트를 시작하고, 게이트가 허용된 동사일 때만 헬퍼를 실행한다. 동사가 끝나면 헬퍼와 게이트가 함께 종료된다.

`status`와 `boot-id`는 헬퍼가 아니다. 게이트가 `hostname`, `uptime`, `whoami`와 `/proc/sys/kernel/random/boot_id`를 직접 읽는다.

## 1. 배치

| 항목 | 값 |
|---|---|
| 경로 | `/usr/local/libexec/gostodian-svc` |
| 소유 | `root:root` |
| 권한 | `0755`. 점검 계정은 실행만 한다 |
| 형태 | 인자로 분기하는 실행 파일 하나. 셸 스크립트여도 된다 |
| 설정 | 서비스 이름, compose 경로, health가 볼 포트, 로그 파일 묶음은 헬퍼 안의 표에 둔다. 저장소와 점검 머신 YAML에는 두지 않는다 |

호출은 셸 문자열로 붙지 않는다. 게이트가 넘기는 인자는 아래가 전부다.

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

`<종류>`는 `dnf`, `boot`, `secure`, `fail2ban`, `audit` 중 하나다. 여기서 `dnf`는 패키지 갱신 로그의 종류 이름이고, `dnf` 바이너리를 실행하는 인자가 아니다. sudoers에는 종류마다 한 줄씩, 서비스마다 `down`과 `start`를 한 줄씩 올린다. `docker`, `dnf *`, `systemd-run`은 sudoers에 두지 않는다. 이 명령은 root로 도는 헬퍼 안에서만 부른다.

`sudo -n`은 비밀번호를 묻지 않는다. sudoers와 인자가 한 글자라도 다르면 sudo가 실패하고, 그 단계는 점검 머신에서 실패가 된다.

## 2. 게이트가 출력을 넘기는 방식

게이트는 `exec`로 헬퍼를 실행한다.

- 종료 코드 0: 헬퍼 stdout만 점검 머신으로 간다. 점검 머신은 본문 전체를 자르지 않고 저장한다.
- 종료 코드 ≠ 0: 게이트가 stdout과 헬퍼 stderr를 버리고, 자신의 stderr에 `동사: exit status N`만 남긴 뒤 1로 끝난다.
- 그래서 실패 이유를 점검 머신에 전하려면 종료 코드 0과 stdout 첫 단어로 보낸다. `update-status`가 이 방식이다. `health`, `down`, `start`, `collect-logs`는 실패를 0이 아닌 종료 코드로 알리므로, 이유는 점검 머신에 닿지 않고 헬퍼가 홈랩 로컬에 남긴다.

한 단계의 제한 시간은 점검 머신 `timeouts.step`이고 기본은 10분이다. 이 안에 헬퍼가 돌아오지 않으면 그 호출은 실패다.

## 3. 공통 규칙

- 알 수 없는 첫 인자, 인자가 더 붙은 경우, 이름에 공백이 있는 경우는 아무것도 실행하지 않고 종료 코드 1이다.
- 서비스 이름은 게이트 허용 목록, 점검 머신 `pipeline.docker_order`, 헬퍼의 이름 표가 모두 같다. 표에 없는 이름은 `down`과 `start`를 하지 않는다.
- 로그 파일을 지우지 않는다. 헬퍼는 그 시점에 있는 파일을 읽기만 한다.
- 가림은 점검 머신이 받는 쪽에서 한다. 헬퍼는 상태 파일과 로컬 진단 로그에 웹훅, API 키, 개인키를 쓰지 않는다.
- 수집이 아닌 단계가 실패하면 다음 단계로 가지 않는다. 이미 끝난 기동은 되돌리지 않는다.

## 4. `down <서비스>`

재부팅 뒤에 서비스가 스스로 올라오지 않게 끈다. `compose stop`은 재시작 정책으로 다시 뜰 수 있으므로 `compose down`으로 끈다.

1. 이름 표에서 그 서비스의 compose 프로젝트, 작업 디렉터리, 서비스 이름을 찾는다.
2. 그 서비스만 내린다. 점검 머신이 `docker_order` 순서대로 한 서비스씩 호출한다.
3. 이미 내려가 있으면 종료 코드 0이다.
4. 내렸으면 종료 코드 0, 내리지 못하면 종료 코드 1이다.

compose 디렉터리는 홈랩 운용자가 표에 적는다.

## 5. `start <서비스>`

`down`과 같은 이름 표로 그 서비스만 올린다. 점검 머신이 `docker_order`의 역순으로 한 서비스씩 호출한다.

1. 표에 있는 그 서비스만 `compose up -d`로 올린다.
2. 이미 떠 있으면 종료 코드 0이다.
3. 뜨지 않으면 종료 코드 1이다. 다음 서비스 기동은 점검 머신이 하지 않는다.

`health`는 `start`가 모두 끝난 뒤에 점검 머신이 따로 호출한다.

## 6. `update`

패키지 갱신을 시작하고 바로 돌아온다. SSH 세션 안에서 `dnf -y update`가 끝날 때까지 기다리지 않는다. 기다리는 쪽은 아래 `update-status` 폴링이다. 폴링이 `timeouts.step`(기본 10분)을 넘기면 점검 머신이 `update-status: timeout`으로 중단한다. 홈랩 갱신이 그보다 길면 점검 머신의 `timeouts.step`을 늘린다.

상태 파일은 `/run/gostodian/update-status`다. 파일에는 단어 하나만 쓴다.

1. 이미 `gostodian-update` 유닛이 돌고 있으면 파일을 바꾸지 않고 종료 코드 0으로 돌아온다.
2. 디렉터리 `/run/gostodian`을 만들고 파일에 `running`을 쓴 뒤 돌아온다. 이 쓰기 전에 종료하면 점검 머신이 빈 상태를 실패로 본다.
3. `systemd-run`으로 일회성 유닛 `gostodian-update`를 만든다. `--wait`는 붙이지 않는다.
4. 유닛이 실행하는 명령은 `dnf -y update` 하나다.
5. 명령의 종료 코드가 0이면 유닛이 파일을 `done`으로, 아니면 `failed`로 덮어쓴다. `done`은 `dnf`가 끝난 뒤에만 쓴다. 도중에 `done`이면 재부팅이 갱신을 끊는다.
6. `systemd-run` 자체가 실패하면 파일을 `failed`로 쓰고 종료 코드 1이다. 유닛을 만들었으면 종료 코드 0이다.

sudoers에는 `systemd-run`과 `dnf`가 없고, 이 헬퍼의 `update` 한 줄만 있다.

## 7. `update-status`

상태 파일을 읽어 stdout에 쓰고 종료 코드는 항상 0이다. 점검 머신은 종료 코드가 0일 때만 첫 단어를 읽고, 대소문자를 가리지 않으며, 그 뒤 단어는 무시한다. 폴링 간격은 2초다.

| 파일 내용 | stdout | 점검 머신 |
|---|---|---|
| `running` | `running` | 2초 뒤 다시 호출 |
| `done` | `done` | 갱신 단계를 끝내고 재부팅으로 진행 |
| `failed` | `failed` | `update-status: failed`로 중단. 재부팅하지 않음 |
| 파일 없음, 빈 파일, 다른 단어 | `failed` | 같은 중단 |

0이 아닌 코드로 끝내면 점검 머신은 단어를 보지 않고 게이트 종료 코드로 실패 처리한다. 상태 의미는 stdout으로만 전달한다.

## 8. `reboot`

호스트를 재부팅한다. 재부팅 전후의 `boot_id` 저장과 비교는 게이트와 점검 머신이 한다. 헬퍼는 재부팅만 시작한다.

1. `systemctl reboot`로 재부팅을 건다.
2. 명령이 0으로 돌아오면 헬퍼도 0으로 끝난다. 재부팅이 세션을 먼저 끊으면 점검 머신은 그 끊김을 성공으로 보고, `boot_id`가 바뀔 때까지 다시 접속한다.
3. 재부팅을 걸지 못하고 0이 아닌 코드로 끝나면 점검 머신은 재접속하지 않고 실패로 중단한다.

## 9. `collect-logs <종류>`

한 호출은 한 종류다. 그 종류의 파일을 순서대로 이어 붙여 stdout으로 내보내고 종료 코드 0이다. 점검 머신은 이 stdout을 `logs/collects/<실행ID>/<종류>`에 저장한 뒤 거르고 가린다.

종류와 홈랩에서 보통 읽는 경로다. 실제 파일 이름은 홈랩에 맞게 표로 확정한다.

| 종류 | 읽는 대상 |
|---|---|
| `dnf` | `/var/log/dnf.log`와 그 회전본. 패키지 갱신 로그 |
| `boot` | `/var/log/boot.log`와 `boot.log-날짜` 회전본 |
| `secure` | `/var/log/secure`와 `secure-날짜` 회전본 |
| `fail2ban` | `/var/log/fail2ban.log`와 `fail2ban.log-날짜` 회전본 |
| `audit` | `/var/log/audit/audit.log`와 그 회전본 |

회전본 이름의 날짜는 그 파일이 닫힌 날이고, 내용의 첫 시각은 그보다 앞일 수 있다. 헬퍼는 날짜로 줄을 잘라내지 않고 있는 파일 전체를 읽는다.

순서:

1. 그 종류의 회전본을 이름 오름차순으로 출력한다.
2. 이어서 현재 파일을 출력한다.
3. 파일 앞에 `# <절대경로>` 한 줄을 붙인다. 본문은 파일 내용 그대로다.
4. 표에 있는 파일이 하나도 없으면 종료 코드 1이다. 점검 머신은 그 종류를 「수집 없음」으로 둔다. 빈 성공(종료 코드 0, 본문 없음)은 「이상 없음」이 되므로, 파일이 없을 때 0으로 끝내면 안 된다.
5. 일부 회전본만 없어도 하나라도 읽었으면 읽은 것을 출력하고 종료 코드 0이다.
6. 읽기 권한이 없거나 도중 오류면 종료 코드 1이다.

이 동사는 sudo로 root가 된다.

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

컨테이너가 떠 있는지, 지정한 포트가 Listening인지 본다. health는 sudo 없이 실행하므로 점검 계정이 도커 소켓을 읽을 수 있어야 한다.

볼 컨테이너와 포트는 헬퍼 안의 표에 둔다. `start`로 올린 집합과 이 표가 같아야 한다.

1. 표의 컨테이너가 모두 running인지 본다.
2. 표의 포트가 모두 Listening인지 `ss -ltn`으로 본다.
3. 모두 맞으면 확인한 컨테이너와 포트를 stdout에 한 줄씩 쓰고 종료 코드 0이다.
4. 하나라도 아니면 종료 코드 1이다. 이 이유는 게이트가 점검 머신으로 넘기지 않으므로, 헬퍼가 `/var/log/gostodian-svc.log`에 어떤 컨테이너나 포트가 실패했는지 남긴다.

`health`가 0이 아니면 점검 머신은 보고서를 저장소에 올리지 않는다.

## 12. 인자 분기

실행 파일의 분기는 이 형태다. compose 경로, 포트, 로그 경로는 홈랩에서 표를 채운 뒤에야 동작한다.

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
      dnf|boot|secure|fail2ban|audit) ;;
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

호출 순서는 아래다. 한 줄이 헬퍼 실행 한 번이다.

1. `down <서비스>`를 `docker_order` 순서대로
2. `update`
3. `update-status`를 `done` 또는 `failed`가 나올 때까지
4. `reboot`
5. 재접속은 게이트의 `boot-id`. 헬퍼를 부르지 않는다
6. `collect-logs dnf`, `collect-logs boot`, `collect-logs secure`, `collect-logs fail2ban`, `collect-logs audit`, `resources`
7. `start <서비스>`를 역순으로
8. `health`

`collect-logs`와 `resources`가 실패해도 점검 머신은 나머지 수집과 `start`, `health`를 계속한다. `down`, `update`, `update-status`, `reboot`, `start`, `health`가 실패하면 다음으로 가지 않는다.

## 14. 예시

서비스가 `minio` 하나일 때의 예시다. 디렉터리와 포트는 홈랩마다 다르므로, 여기 경로를 그대로 계약으로 두지 않는다. 헬퍼 안의 표만 홈랩 값으로 채운다.

이름 표:

| 항목 | 예시 |
|---|---|
| 서비스 이름 | `minio` |
| compose 프로젝트 | `gostodian` |
| 작업 디렉터리 | `/home/developer/gostodian-minio` |
| compose 파일 | `/home/developer/gostodian-minio/compose.yaml` |
| compose 서비스 | `minio` |
| 컨테이너 | `gostodian-minio` |
| 포트 | `9000`, `9001` |

점검 머신 `pipeline.docker_order`와 게이트 허용 목록도 `minio` 하나다. sudoers 예시는 아래다. `update-status`, `health`, `resources`는 이 목록에 없다.

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

`down minio`가 부르는 명령:

```sh
docker compose --project-name gostodian --project-directory /home/developer/gostodian-minio -f /home/developer/gostodian-minio/compose.yaml down minio
```

`start minio`가 부르는 명령:

```sh
docker compose --project-name gostodian --project-directory /home/developer/gostodian-minio -f /home/developer/gostodian-minio/compose.yaml up -d minio
```

볼륨 옵션 `-v`는 붙이지 않는다. `down`은 컨테이너만 지우고 데이터 볼륨은 남긴다.

`update-status` stdout은 단어 하나이고 종료 코드는 0이다.

```
running
```

```
done
```

```
failed
```

`health`가 맞을 때 stdout 예시다. 한 줄에 하나다.

```
container gostodian-minio
port 9000
port 9001
```

`resources` stdout 예시다. 숫자는 그 시점의 값이다.

```
RAM
MemTotal: 3723796 kB
MemAvailable: 2988132 kB
CPU
loadavg: 3.95 1.11 0.38
nproc: 1
Storage
/dev/mapper/ocivolume-root   30G   12G   18G  41% /
/dev/sda2                   2.0G  952M  1.1G  48% /boot
```

`collect-logs secure` stdout의 시작 예시다. 첫 줄은 읽은 파일의 절대경로이고, 다음 줄부터가 본문이다. 회전본이 있으면 그 경로 줄이 먼저 온다.

```
# /var/log/secure-20260809
Aug  2 18:51:00 bluenyang-vnic-01 sshd-session[1662439]: Connection closed by 10.90.0.3 port 52486
# /var/log/secure
Nov  7 14:25:26 bluenyang-vnic-01 sshd-session[4694]: Accepted publickey for gostodian from 10.90.0.3 port 58062 ssh2: ED25519 SHA256:...
```
