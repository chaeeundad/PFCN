# 바로 시작하기

Pumat 노드를 설치해서 **계산 자원을 기여**하거나 **계산을 맡기는** 데까지 15분 안에 끝내는 안내입니다.
개념 소개는 [소개 슬라이드](https://chaeeundad.github.io/PFCN/slides/)를, 프로토콜 전체는 [스펙](architecture.md)을 보세요.

> 상태: pre-alpha (`v0.1.0-alpha.1`). 공개 부트스트랩 노드가 아직 없어서 **같은 LAN 안에서는 자동으로 서로를 찾고**, 다른 네트워크의 노드는 주소(`--peer`)로 연결합니다.

## 0. 역할 고르기

| 역할 | 하는 일 | 필요한 것 |
|---|---|---|
| **워커** (기여자) | 남는 CPU로 다른 사람의 계산을 실행 | Linux, Docker 또는 Podman |
| **요청자** | 계산을 맡기고 결과를 받음 | Linux 또는 macOS (컨테이너 불필요) |

한 컴퓨터가 두 역할을 함께 해도 됩니다.

## 1. 설치

미리 빌드된 바이너리를 설치합니다. Go도 컴파일도 필요 없습니다.

```bash
curl -fsSL https://raw.githubusercontent.com/chaeeundad/PFCN/main/scripts/install.sh | sh
pumat version
```

- 설치 스크립트는 [릴리스](https://github.com/chaeeundad/PFCN/releases)의 체크섬을 확인합니다. [`cosign`](https://docs.sigstore.dev/cosign/system_config/installation/)이 있으면 서명과 서명자(이 저장소의 릴리스 워크플로)도 확인합니다.
- 서명 확인을 필수로 하려면 `REQUIRE_SIGNATURE=1`을 붙입니다.
- Linux에서 `sudo`로 실행하면 전용 `pumat` 사용자와 systemd 유닛도 만듭니다. 시작은 하지 않습니다.
- 자원 공유는 `pumat on`을 직접 실행하기 전까지 켜지지 않습니다.

솔버 정보와 예제를 받습니다.

```bash
mkdir -p ~/pumat-start && cd ~/pumat-start
R=https://raw.githubusercontent.com/chaeeundad/PFCN/main
curl -fsSLO $R/solvers/quantum-espresso/manifest.signed.json
curl -fsSLO $R/examples/qe-si-scf/job.yaml
curl -fsSLO $R/examples/qe-si-scf/silicon.cif
curl -fsSLO $R/examples/qe-si-scf/fetch-pseudo.sh && chmod +x fetch-pseudo.sh
```

<details><summary>소스에서 빌드하기 (개발자용)</summary>

[Go 1.27 이상](https://go.dev/dl/)이 필요합니다.

```bash
git clone https://github.com/chaeeundad/PFCN.git && cd PFCN
make build && sudo install bin/pumat /usr/local/bin/pumat
```

소스 트리에서는 아래 명령의 파일 경로를 `solvers/quantum-espresso/manifest.signed.json`, `examples/qe-si-scf/...`로 바꿔 쓰면 됩니다.
</details>

## 2. 노드 초기화 (워커·요청자 공통)

```bash
pumat init
pumat solver add manifest.signed.json
```

- `init`은 `~/.pumat/`에 노드 신원(Ed25519 키)과 설정 파일을 만듭니다. 기본값은 **CPU 코어 절반, 메모리 1/4 제공, 공유 꺼짐**입니다.
- `solver add`는 프로젝트가 서명한 Quantum ESPRESSO 솔버 정보를 설치합니다. 서명은 `pumat init`이 기본으로 신뢰하는 프로젝트 키로 확인합니다.

에이전트(백그라운드 데몬)를 띄웁니다. CLI는 이 에이전트와 통신합니다.

```bash
pumat agent > ~/.pumat/agent.log 2>&1 &
pumat status
```

서버라면 systemd로 띄우는 것을 권장합니다. 유닛 파일은 [`deploy/systemd/pumat-agent.service`](https://github.com/chaeeundad/PFCN/blob/main/deploy/systemd/pumat-agent.service)에 있습니다.

## 3. 워커: 계산 자원 기여하기

먼저 샌드박스가 제대로 격리되는지 점검합니다.

```bash
pumat doctor --image ghcr.io/chaeeundad/pumat-quantum-espresso@sha256:7ea3d7fb93f904b435071ceb6e3797a4cfc57ec0e848cf124ad43e6c28d18d54
```

ARM 서버라면 이미지 digest를 `sha256:34a423156bc78375f2c2f6cab6a97eb66133be7be2da9ea98815c361a9b984da`로 바꾸세요.
`sandbox smoke test ... network denied`가 나오면 준비된 것입니다.

제공할 자원을 정하고 공유를 켭니다.

```bash
pumat resources set --cpu 8 --memory 16GiB    # 바꾼 뒤 에이전트 재시작
pumat on
```

| 명령 | 동작 |
|---|---|
| `pumat on` | 새 작업 받기 시작 |
| `pumat off` | 새 작업 받지 않음 (실행 중인 작업은 끝까지) |
| `pumat status` | 상태와 **다른 사람에게 알려줄 주소** 표시 |
| `pumat job list` | 실행했거나 실행 중인 작업 |
| `pumat receipts list` | 서명된 계산 영수증 |

워커가 지키는 것:
- 서명된 승인 솔버만 실행하고, 임의 명령은 실행하지 않습니다.
- 네트워크 차단·읽기 전용 루트·권한 제거·root 실행 거부를 적용합니다. CPU·메모리·프로세스·디스크·시간에 한도가 걸립니다.
- 결과를 요청자 키로 봉인한 즉시 입력·출력 평문을 지웁니다. 남는 것은 요청자만 열 수 있는 암호문이고, 그것도 전달되거나 보관 기한이 지나면 삭제됩니다.

> Docker 데몬이 root로 돈다면 `doctor`가 경고합니다. 가능하면 rootless Podman을 쓰세요.

## 4. 요청자: 첫 계산 맡기기

예제는 실리콘(Si) 2원자 SCF 계산입니다.

```bash
./fetch-pseudo.sh                 # 의사퍼텐셜 다운로드 + 해시 검증
pumat submit job.yaml
```

```text
Discovering compatible workers...
2 currently eligible
Lease accepted by peer 12D3Ko...1tEP
Uploading encrypted input from local disk...
...
State:     COMPLETED
Converged:     true (7 SCF steps)
Total energy:  -310.569142 eV
```

- **같은 LAN**: 워커를 자동으로 찾습니다.
- **다른 네트워크**: 워커의 `pumat status`에 나온 주소를 넣습니다. 워커 쪽 UDP/TCP 4001 포트가 열려 있어야 합니다.

  ```bash
  pumat submit job.yaml --peer /ip4/203.0.113.10/udp/4001/quic-v1/p2p/12D3KooW...
  ```

### 오래 걸리는 계산: 올려두고 닫기

```bash
pumat submit job.yaml --detach        # 입력 업로드가 끝나면 바로 돌아옴
pumat job list --pending              # 나중에 확인
pumat job fetch <exec-id>             # 즉시 회수 시도 (에이전트도 자동으로 회수)
pumat job cancel <exec-id>            # 봉인 전이면 취소
```

워커는 결과를 기본 24시간 동안 암호문으로 보관합니다. 그 안에 에이전트가 한 번만 켜지면 결과를 자동으로 받습니다.

### 결과 확인

```bash
ls ~/pumat/results/<exec-id>/
#   input/       제출한 입력 (canonical-job.json, 구조, 의사퍼텐셜)
#   output/      원시 출력 (stdout.txt, data-file-schema.xml)
#   parsed/      해석 결과 result.json (에너지, 힘, 응력, 최종 구조)
#   provenance/  계약·영수증·솔버 매니페스트·실행 환경
pumat receipt verify <exec-id>        # 서명과 결합을 오프라인으로 검증
```

## 5. 내 계산 만들기

[`examples/qe-si-scf/job.yaml`](https://github.com/chaeeundad/PFCN/blob/main/examples/qe-si-scf/job.yaml)을 복사해서 고칩니다.

```yaml
metadata:
  name: my-calc
  visibility: private          # public | unlisted | private (기본 private)
resources:
  cpu: {cores: 4}
  memory: 8GiB
  walltime: 2h
system:
  structure: {file: my.cif}    # 대칭 연산이 있거나 P1인 CIF, 원자 200개 이하
  pseudopotentials:
    Fe: {filename: Fe.pbe-spn-rrkjus_psl.1.0.0.UPF, digest: "sha256:..."}
calculation:
  type: relax                  # scf | relax | vc-relax
  parameters: {ecutwfc: 45, ecutrho: 360, kpoints: [6, 6, 6], occupations: smearing, smearing: mv, degauss: 0.02}
```

- 의사퍼텐셜 파일은 job 파일 옆이나 `pseudo/` 폴더에 둡니다. digest는 다음 명령으로 얻습니다.

  ```bash
  shasum -a 256 Fe.pbe-spn-rrkjus_psl.1.0.0.UPF     # 출력 앞에 sha256: 붙이기
  ```

- 허용된 QE 파라미터만 쓸 수 있습니다. `outdir`, `pseudo_dir`, `prefix` 같은 경로·재시작 설정은 Pumat이 고정하므로 넣으면 거부됩니다. 허용 목록은 스펙 §39.1에 있습니다.
- `command`, `shell`, `image` 같은 실행 관련 필드는 어떤 형태로든 거부됩니다.
- 처음 쓰는 워커에서는 신규 요청자 제한 때문에 walltime이 2시간 이하여야 합니다. 그 워커에서 작업 3개가 완료되면 풀립니다.

## 6. 결과 공개와 재현 (선택)

`visibility: public`인 작업만 공개할 수 있습니다. private 작업은 절대 공개되지 않습니다.

```bash
pumat job publish <exec-id>           # 서명된 공개 레코드 생성 + 네트워크에 공지
pumat reproduce <record-id>           # 원래 워커를 제외한 다른 워커에서 재실행 후 비교
pumat record verify ~/.pumat/records/<hex>   # 받은 레코드를 오프라인 검증
```

재현 결과는 `REPRODUCED_EXACT`, `REPRODUCED_WITHIN_TOLERANCE`, `DIVERGENT` 중 하나로 서명됩니다.

### 익스플로러 띄우기

`~/.pumat/config.yaml`에서 다음과 같이 설정하고 에이전트를 재시작합니다.

```yaml
indexer:
  enabled: true
  http: 127.0.0.1:8080
```

브라우저에서 http://127.0.0.1:8080 을 열면 공지된 공개 레코드를 검색할 수 있습니다. API는 `/api/records?formula=Si`입니다.

## 7. 다른 네트워크의 노드와 연결하기

공개 IP가 있는 노드 하나를 부트스트랩으로 정하고, 다른 노드들의 `~/.pumat/config.yaml`에 그 주소를 넣으면 `--peer` 없이 서로를 찾습니다.

```yaml
network:
  bootstrap:
    - /ip4/203.0.113.10/udp/4001/quic-v1/p2p/12D3KooW...
```

부트스트랩·relay 노드 운영 방법은 [`deploy/bootstrap/README.md`](https://github.com/chaeeundad/PFCN/blob/main/deploy/bootstrap/README.md)에 있습니다.

## 문제 해결

| 증상 | 원인과 해결 |
|---|---|
| `cannot reach the local agent` | 에이전트가 꺼져 있음 → `pumat agent &` |
| `no workers found for this solver` | 같은 LAN에 `on` 상태 워커가 없음 → `--peer`로 주소 지정, 또는 `network.bootstrap` 설정 |
| `worker is paused` / `busy` | 워커가 `off`이거나 동시 작업 한도에 걸림 |
| `solver manifest ... is not installed` | `pumat solver add manifest.signed.json` (1단계에서 받은 파일) |
| `pseudopotential ... digest mismatch` | 파일이 job에 고정한 digest와 다름 → 파일 확인 또는 digest 갱신 |
| `parameter "..." is not in the allowlist` | 허용되지 않은 QE 파라미터 (스펙 §39.1) |
| `new requesters are limited to ...` | 처음 쓰는 워커의 walltime 제한 → walltime 줄이기 |
| 다른 네트워크에서 연결 안 됨 | 워커의 UDP/TCP 4001 방화벽 확인, `pumat network diagnose` |
| 계산이 `FAILED` | `pumat job status <id>`의 Error 확인. 워커 쪽은 `~/.pumat/agent.log` |

## 명령 한눈에 보기

```text
pumat init | agent | status | on | off | doctor
pumat submit <job.yaml> [--peer ...] [--detach]
pumat job list | status | wait | fetch | cancel | publish <id>
pumat receipt show|verify <id>      pumat ledger verify
pumat reproduce <record-id>         pumat record fetch|verify
pumat solver add|list|verify        pumat revocations add|show
pumat network diagnose | peers      pumat reputation
```
