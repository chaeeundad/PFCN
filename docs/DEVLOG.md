# Pumat 개발 이력 (DEVLOG)

중요한 결정, 구현 내역, 사용자 조치 필요 사항을 시간순으로 남깁니다.
설계 결정의 근거는 [docs/adr/](adr/)에, 프로토콜 정의는 [architecture.md](architecture.md)에 있습니다.

---

## 현재 상태 요약 (2026-10-02 기준)

| 단계 (스펙 §48) | 상태 | 비고 |
|---|---|---|
| Phase 0 프로토콜 골격 | 완료 | JCS, 서명 envelope, ID, 신원, CLI |
| Phase 1 두 노드 실행 | 완료 | 실제 QE 컨테이너 E2E, 봉인 결과, 영수증 |
| Phase 2 연합 탐색 | 완료 (로컬 검증) | DHT/mDNS/hole punching/relay. 공개 부트스트랩 노드 없음 (U6) |
| Phase 3 보안 강화 | 대부분 | 리보케이션, 퍼징, 디스크 워치독, govulncheck. **미완**: 워크스페이스 암호화(루트 헬퍼 필요), 솔버 매니페스트 Sigstore 검증 |
| Phase 4 공개 레코드 | 완료 | 게시, 미러링, 공지, 재현, 익스플로러. §58 데모 실제 수행 |
| Phase 5 공개 알파 | 기반 완료 | 서명 릴리스 워크플로, 설치 스크립트, systemd, 부트스트랩 문서. **대기**: 첫 태그(U7), 실제 리눅스 다중 머신(U3) |
| Phase 6 GPU + 두 번째 솔버 | 보류 | **GPU는 범위에서 제외** (2026-10-02 결정). 두 번째 솔버는 이후 검토 |
| Phase 7 공정성·평판 | 일부 | 로컬 평판 차원, 신규 요청자 제한, 신뢰도 기반 후보 정렬 |
| Phase 8 기관 연합 | 미착수 | 네임스페이스·멤버십·기관 증명 |

v0.1 완료 정의(§65) 대비: 1(서명 릴리스 설치)은 첫 태그 후 가능, 2~13은 로컬에서 충족 확인. 단 "서로 다른 네트워크의 독립 관리 머신"은 U3·U6 이후.

## 사용자 조치가 필요한 항목 (열린 목록)

| # | 항목 | 이유 | 상태 |
|---|---|---|---|
| U1 | `secrets/solver-signing.key` 보관 | 프로젝트 솔버 서명 키(신뢰 루트, ADR-0009) | 해결: 이 로컬 머신에 보관하기로 결정 (2026-10-02) |
| U2 | ~~GHCR 토큰~~ | GitHub Actions의 `GITHUB_TOKEN`으로 이미지 게시 → 개인 토큰 불필요. 패키지는 공개 repo에 연결되어 익명 pull 가능 확인 | 해결 (2026-10-02) |
| U3 | Linux 머신 2대 이상에서 실제 다중 노드 테스트 | 현재 검증은 macOS(OrbStack) 한 대에서 두 노드. 서로 다른 네트워크 간 연결은 Phase 2 이후 | 대기 |
| U4 | 라이선스 결정 | 코드 Apache-2.0, 스펙·문서 CC BY 4.0 | 해결: 제안대로 승인 (2026-10-02) |
| U5 | (로컬 환경) Docker 자격 증명 헬퍼 | 이 Mac에서 `docker-credential-osxkeychain`이 키체인 프롬프트로 멈춤. 개발 중에는 별도 `DOCKER_CONFIG`로 우회함. Docker Desktop/OrbStack 설정에서 credsStore를 정리하면 해결 | 참고 |

---

## 2026-10-02 — Phase 0 + Phase 1 구현

### 범위
스펙 §64 "Initial milestone": 두 Linux 머신 간 `pumat submit --peer` → QE 실행 → 결과 회수 → 양측 서명 영수증.
macOS 개발 머신에서 실제 QE 컨테이너로 두 노드 E2E를 통과함 (`make e2e`).

### 구현된 것
- **서명/식별자**: RFC 8785 JCS(부동소수점 금지), §7.4.1 서명 envelope(도메인 분리), 타입 ID `pumat:<type>:blake3:<hex>`, `derive()` 해시
- **신원**: Ed25519 키(0600, 권한/손상 시 fail-closed), libp2p Peer ID
- **작업 스키마**: 엄격 YAML(앵커·별칭·커스텀 태그·병합키·중복키·미지 필드·금지 필드 거부), 단위 정규화, `calc_id`(런타임/파일명 제외, 내용 digest만 포함)
- **QE 어댑터**: 파라미터 allowlist, 어댑터 고정 파라미터(outdir/pseudo_dir/prefix/disk_io/max_seconds), CIF 파서(대칭 연산 전개, 부분 점유 거부, 원자 200개 제한), `pw.in` 생성, k-point 풀 추정
- **솔버 신뢰**: 서명된 솔버 매니페스트, 보안 하한(네트워크 deny/readOnly/비특권), 신뢰 서명자 정책(빈 목록 = 아무것도 신뢰 안 함)
- **샌드박스**: podman 우선/docker, `--network none --read-only --cap-drop ALL --no-new-privileges`, CPU/메모리(스왑 금지)/PID/walltime 제한, root 실행 거부, 이미지는 digest로만
- **전송**: 1 MiB 청크 + BLAKE3 머클 루트, 청크별 검증, have-map으로 재개 가능
- **결과 봉인**: HPKE export-only + ChaCha20-Poly1305 청크 인덱스 nonce (ADR-0011). 봉인 즉시 워커 평문 삭제
- **비동기 회수**: attached(3초 폴링)/detached(5분 + `job fetch`), 요청자 재시작 후 자동 회수, 보관 기한 만료 처리
- **영수증**: 워커 completion + 요청자 acceptance, `receipt verify`(오프라인 JSON 검증 가능, 변조 탐지 확인)
- **이벤트 체인**: 모든 상태 전이를 서명된 해시 체인으로 SQLite에 기록, `ledger verify`
- **파서**: QE XML 출력 → 정규 결과 JSON, WASI 모듈로 빌드(재현 가능 digest 확인) 후 wazero로 요청자 측 실행
- **CLI**: init/agent/status/on/off/submit/job/receipt(s)/ledger/solver/resources/doctor
- **테스트**: 단위 + 통합(인프로세스 두 노드, fake runtime) + race 검사 + 실제 컨테이너 E2E 스크립트

### 주요 결정 (스펙과 다르거나 스펙이 정하지 않은 부분)
1. **솔버 서명은 Phase 1에서 자체 Ed25519 envelope 사용** (ADR-0009). Sigstore는 Phase 3에서 추가 필수 검사로 도입.
   프로젝트 서명자 ID: `12D3KooWNmqy7RJXuA8VKQDgfcWhhAtiVPpwXGRQA3K2VvaDRKeP` (기본 신뢰 루트).
2. **봉인 구성**: 스펙의 "HPKE ChaCha20-Poly1305"를 export-only HPKE + 청크별 ChaCha20-Poly1305로 구체화 (ADR-0011). 스펙 §16.6 갱신.
3. **QE outdir는 `/work/scratch`** (스펙은 `/work/out`): 파동함수 등 대용량 스크래치가 결과 번들에 섞이지 않도록 분리. `/work/out`에는 stdout/stderr/`data-file-schema.xml`만. 스펙 §39.1 갱신.
4. **calculation.type은 scf/relax/vc-relax만 허용**: nscf/bands는 이전 실행 상태가 필요해 단일 작업으로 불가. 다단계 워크플로 이후로 연기. 스펙 §12.2 갱신.
5. **파서는 Phase 1에서 에이전트에 내장** (레지스트리 fetch는 Phase 4). 매니페스트의 parser digest가 내장 파서와 다르면 제출 거부.
6. **워커 측 `HELD` 상태는 `SEALED`로 통합**, 요청자 측 `DELIVERED`/`ACKNOWLEDGED` 의미 명시. 스펙 §15.2에 주석.
7. **custodian, 입력 사전 스테이징, 작업 취소, GPU는 미구현**: 요청 시 명확한 오류로 거부(placeholder 아님).
8. **워크스페이스 암호화(§16.5)는 Phase 3**: 그전까지 capability에 `workspace_encryption: false`로 정직하게 광고.
9. **에이전트 재시작 시 RUNNING 작업은 FAILED 처리** 후 워크스페이스 삭제. 요청자는 FAILED로 보고받음.
10. **Unix 소켓 경로 길이 제한(104바이트)** 대응: 홈 경로가 길면 임시 디렉터리에 해시 이름으로 소켓 생성.
11. **BLAS 재현성**: Debian OpenBLAS + 런타임 `OPENBLAS_CORETYPE` 고정(amd64 NEHALEM, arm64 ARMV8).

### 검증 결과 (이 머신, Apple M4 Pro / OrbStack, linux/arm64)
- Si SCF(2원자, 4×4×4 k, ecutwfc 30 Ry, 2 MPI): 제출→완료 약 4초, 총에너지 −310.569142 eV, 7 SCF 스텝 수렴
- detached 제출 → 요청자 에이전트 종료 → 워커는 `sealed/`만 보유 → 요청자 재시작 시 자동 회수·완료 확인
- 영수증 JSON의 필드 1개 변조 시 `receipt verify`가 서명 오류로 거부
- 완료 후 워커 work 디렉터리 비어 있음, 요청자 수신 키 삭제됨, 컨테이너 제거됨

### 남은 작업 (다음 단계)
- Phase 2: 부트스트랩/DHT/AutoNAT/hole punching/relay, `--peer` 없이 후보 탐색
- Phase 3: 리보케이션 목록, Sigstore 검증, 워크스페이스 암호화, 디스크 쿼터, 디코더 퍼징
- Phase 4: 공개 레코드 번들/공지, 인덱서, 웹 익스플로러
- Phase 5: 설치 스크립트, systemd 유닛, 서명된 릴리스

---

## 2026-10-02 — Phase 2 (탐색) + GHCR 게시 + Phase 3 일부

### GHCR 인증 질문에 대한 정리
- 키체인의 GitHub 토큰(`gho_…`, OAuth)은 scope가 `repo, workflow, read:user, user:email`이라 `write:packages`가 없어 로컬에서 GHCR push 불가.
- 대신 `.github/workflows/solver-image.yml`이 Actions의 `GITHUB_TOKEN`(`packages: write`)으로 게시. 워크플로 실행은 키체인 토큰의 `workflow` scope로 API 디스패치.
- 결과: `ghcr.io/chaeeundad/pumat-quantum-espresso`
  - linux/amd64 `sha256:7ea3d7fb…d54`, linux/arm64 `sha256:34a42315…4da`, index `sha256:65f51b43…842`
  - 익명 pull 가능 (공개 repo 연결로 공개 상태 상속)
- 서명은 오프라인(로컬 프로젝트 키) 유지. 공개 매니페스트 digest: `sha256:8b9f313c30ca190effbde43d89d95d9bbcdd0d6f16b7baacbe7798868b009d92`
- 예제 job.yaml이 이 digest를 고정 → 기본 설정의 새 노드에서 바로 실행 가능

### Phase 2 구현
- `/pumat` 프로토콜 접두사의 전용 Kad-DHT (IPFS 공용 DHT와 분리), `dht.DisableValues()` (provider record만 사용)
- 워커는 available일 때 솔버 매니페스트별 provider record 공지(1시간 주기 재공지, `on` 시 즉시)
- mDNS: 동시에 서로 다이얼하면 TCP simultaneous open으로 보안 핸드셰이크가 깨지는 문제 발견 → 작은 peer ID 쪽만 다이얼, 연결 실패 시 1회 재시도(backoff 우회)
- NAT: `NATPortMap`, hole punching(DCUtR), 정적 relay(AutoRelay), 선택적 relay 서비스
- `pumat submit`에서 `--peer` 생략 시 후보 탐색 → 서명된 capability 검증 → 직접 연결 우선·가용 코어 순 정렬 → 거절 시 다음 후보
- 오래된 주소는 DHT `FindPeer`로 재해석
- **기본 부트스트랩 목록은 비어 있음**: 프로젝트 부트스트랩 서버가 아직 없음 (U6 참고). 같은 LAN은 mDNS로 동작

### Phase 3 일부
- 서명된 리보케이션 목록(`pumat.revocation.v1`): 매니페스트/아티팩트/서명자 단위, sequence 롤백 방지, https URL 주기 갱신, `requireRevocationList`(기본 false: 아직 공개 목록 없음)
- 워커(lease, 실행 직전)와 요청자(제출) 모두에서 검사
- 퍼징 6종(JCS, envelope, CIF, job YAML, tar 추출, 프레임 디코딩) 각 20초 통과, CI에서는 seed corpus로 실행
- **파서 재현성 버그 수정**: Go가 바이너리에 VCS 정보를 박아 넣어 CI 재빌드 digest가 달라짐 → `-buildvcs=false`. macOS arm64와 Linux amd64 컨테이너 빌드 digest 일치 확인 (`sha256:6257a438…5f5`)

### 검증
- 기본 설정 새 노드 2개: mDNS로 자동 탐색 → GHCR에서 익명 pull → 실행 → 영수증 완료 (약 12초, 이미지 pull 포함). 에너지는 로컬 빌드 이미지와 동일 (−310.569142 eV)
- 3노드(부트스트랩+워커+요청자, mDNS 끔) DHT 탐색 통합 테스트 통과
- `-race` 전체 통과

### 사용자 조치 추가
| # | 항목 | 이유 |
|---|---|---|
| U6 | 공개 부트스트랩/relay 노드 | 1호기 운영 중 (Lightsail 싱가포르 52.77.24.240). 2호기는 다른 리전에 추가 예정 |

---

## 2026-10-02 — Phase 4 (공개 과학 레코드) + Phase 5 일부

### Phase 4 구현
- `pumat job publish <exec>`: §22.2 레이아웃의 레코드 번들 + 게시자 서명 `pumat.record.v1` 매니페스트
  - `record_id = pumat:record:blake3(<매니페스트 payload>)`
  - job의 publication 플래그(input/rawOutput/parsedOutput/provenance)를 지킴. `input/job.yaml`은 로컬 주석/경로가 있을 수 있어 제외
  - **private 작업은 게시 거부** (조용히 공개하지 않음, §22.3)
  - 검색용 요약(화학식·원소·에너지 등)은 부동소수점 금지 규칙에 따라 십진 문자열
- 레코드 배포: 보유한 모든 노드가 `/pumat/record/1.0.0`으로 제공 + DHT provider record. 가져간 노드는 자동으로 미러가 됨
- 공지: GossipSub `/pumat/<ns>/records/v1` (검증자에서 서명·public 여부 확인) + `publication.indexers`로 직접 push
- `pumat record fetch|verify`: 오프라인 검증 (서명, 모든 파일 해시, lease→completion→acceptance 체인과 매니페스트의 결합)
- `pumat reproduce <record>`: 레코드의 canonical job을 **원래 워커를 제외한** 다른 워커에서 재실행
  - parsed digest가 같으면 `REPRODUCED_EXACT`, 아니면 에너지·힘 허용오차로 판정 (기본 1e-4 eV, 1e-3 eV/Å)
  - 비교 결과는 재현 레코드 매니페스트에 서명되어 포함
- 인덱서/익스플로러(`indexer.enabled: true`): SQLite(스펙은 PostgreSQL, MVP에서는 SQLite로 충분하다고 판단), 검색 페이지, 레코드 페이지(§22.5), REST API, 파일 다운로드
  - 재빌드 시 동일 결과 재현 (테스트로 확인)
  - unlisted 레코드는 인덱싱하지 않음
  - "agreement ≠ scientific correctness" 문구 표시
- 샌드박스 디스크 워치독: bind mount는 크기 제한을 걸 수 없어 10초마다 scratch+out 용량을 측정해 초과 시 종료 (`resource_exceeded`)

### §58 첫 데모 실제 수행 (이 머신, 실제 QE 컨테이너)
A(요청자+익스플로러), B·C(워커):
1. A가 `--peer` 없이 제출 → mDNS로 2개 워커 발견 → B에서 실행 → 완료
2. 게시 → 익스플로러에 `Si scf −310.569142 eV` 표시
3. `pumat reproduce` → B 제외, C에서 실행 → `REPRODUCED_EXACT` (ΔE = 0)
4. 재현 레코드 게시 → 익스플로러 재현성: 실행 2회, `REPRODUCED_EXACT`

### Phase 5 일부
- `release.yml`: 태그 `v*` 푸시 시 4개 플랫폼 빌드 → `SHA256SUMS` → Sigstore keyless 서명(워크플로 OIDC 신원) → GitHub prerelease
- `scripts/install.sh`
  - OS/아키텍처 감지, 체크섬 검증, cosign이 있으면 서명과 서명자 신원(이 repo의 release 워크플로) 검증
  - Linux root 실행 시 `pumat` 사용자·subuid/subgid·linger·systemd 유닛 생성
  - 자원 공유는 켜지 않음
- `deploy/systemd/pumat-agent.service`: 비특권 사용자, `Delegate=yes`(cgroup 위임), 하드닝 옵션
- `deploy/bootstrap/`: 부트스트랩/relay 노드 설정 예시와 절차 (같은 바이너리, `dhtMode: server`, `relayService: true`)
- `pumat update check` (설치는 검증하는 설치 스크립트로만, 자동 실행 없음)
- SECURITY.md / CONTRIBUTING.md / GOVERNANCE.md / CODE_OF_CONDUCT.md

### CI 이슈와 수정
- e2e 실패: Ubuntu 러너에 Podman이 있어 에이전트가 Podman을 골랐는데 이미지는 Docker로 푸시됨 → e2e 스크립트가 노드의 `runtime.engine`을 사용 엔진으로 고정
- 부수적으로 Podman 자동 선택이 실제로 동작함을 확인

### 사용자 조치 추가
| # | 항목 | 이유 |
|---|---|---|
| U7 | 첫 릴리스 태그 | 해결: `v0.1.0-alpha.1` 게시 (2026-10-02) |

---

## 2026-10-02 — 작업 취소, 로컬 평판 (Phase 7 일부)

- `pumat job cancel <exec>`: 요청자가 서명한 `pumat.cancel.v1`를 결과 프로토콜로 전송 → 워커가 서명자=lease 요청자 확인 후 컨테이너 중지·워크스페이스 삭제, 양측 `CANCELLED`. 봉인 이후에는 취소 불가(결과는 이미 암호문)
- 로컬 평판(§35): 피어별 완료/실패/유실/분쟁/거절 횟수와 감쇠 사용량(τ = 7일, §19.4)을 **각각 별도 차원으로** 보관. 전역 단일 점수는 만들지 않음
  - 요청자: 후보 정렬 = 직접 연결 > 로컬 신뢰도(라플라스 평활 성공률) > 가용 코어
  - 워커: 이 노드에서 완료 이력이 `jobs.trustedAfter`(기본 3) 미만인 요청자는 `jobs.newRequesterMaxWalltime`(기본 2h)로 제한 (Sybil 완화, §35.3)
  - `pumat reputation`으로 확인
- 작업 보존형 공정성(work-conserving)은 현재 "유휴면 누구든 수락" 형태. 대기열 기반 경합 조정은 오퍼 풀(job board, §14.4 후속)이 생긴 뒤 구현

---

## 2026-10-02 — CI 전부 통과

- e2e 2차 원인: Ubuntu 러너의 클래식 Docker는 OCI index가 아니라 Docker v2 매니페스트를 푸시함. 스크립트가 셸 변수로 재인코딩된 본문을 해시해서 레지스트리에 없는 digest가 나옴 → 레지스트리의 `Docker-Content-Digest` 헤더를 사용하도록 수정
- 커밋 `770c108`에서 CI 전체 통과: lint, `-race` 테스트, 파서 재현성, govulncheck, 크로스 컴파일, **GitHub Linux amd64 러너에서 실제 QE 컨테이너 2노드 E2E**
- 이로써 linux/arm64(이 Mac)와 linux/amd64(CI) 샌드박스 경로 모두 실측 검증

---

## 2026-10-02 — 결정 사항 반영, 소개 슬라이드

- 사용자 결정: U1은 로컬 보관으로 해결, U3~U7은 사용자가 직접 진행, **GPU는 당분간 범위에서 제외**
  - 따라서 GPU 관련 스펙(§17.4, Phase 6 GPU)은 구현하지 않음. 작업 스키마는 계속 `gpu.count: 0`만 허용
- 프로젝트 소개 슬라이드: `docs/slides/index.html` (12장, 한국어)
  - 의존성 없는 단일 HTML. 키보드·스와이프 이동, 전체화면(F), 인쇄→PDF(P)
  - GitHub Pages(`main` 브랜치 `/docs`)로 게시: https://chaeeundad.github.io/PFCN/slides/
  - 내용: 문제 → 품앗이 아이디어 → 동작 흐름 → 안전 설계 → detached 경험 → 영수증 → 재현 → 네트워크 효과 → 실측 결과·진행 상황 → 참여 방법
  - 수치는 실제 측정값(Si SCF 4초, linux/arm64·amd64 실측, 재현 ΔE 0)만 사용

---

## 2026-10-02 — 바로 시작하기 문서

- `docs/QUICKSTART.md` (한국어): 역할 선택 → 설치 → 초기화 → 워커 기여 → 첫 계산 → detached → 결과 구조 → 내 job 작성 → 공개·재현·익스플로러 → 다른 네트워크 연결 → 문제 해결
- 문서의 명령 순서를 새 노드 2개에서 그대로 실행해 확인 (submit --detach → job fetch → COMPLETED, receipt verify 통과)
- 첫 릴리스 전까지는 소스 빌드(Go 1.27+) 안내. 릴리스 후 설치 스크립트로 교체 예정 (U7)

---

## 2026-10-02 — 라이선스 확정, 첫 릴리스

- 사용자가 제안을 승인: **코드 Apache-2.0, 스펙·문서 CC BY 4.0, 첫 태그 `v0.1.0-alpha.1`**
- `LICENSE`(apache.org 공식 원문), `NOTICE`(서드파티 고지: QE는 GPL-2.0-or-later로 별도 컨테이너 이미지로만 실행, 의사퍼텐셜 미재배포), `docs/LICENSE.md` + CC BY 4.0 원문
- 릴리스 아카이브에 LICENSE·NOTICE 포함
- `v0.1.0-alpha.1` 태그 → release 워크플로 성공. 4개 플랫폼 아카이브 + `SHA256SUMS` + Sigstore 번들, pre-release로 게시
- 설치 스크립트 실측:
  - macOS arm64에서 `REQUIRE_SIGNATURE=1`로 서명·체크섬 검증 통과 후 설치
  - Linux(alpine 컨테이너)에서 체크섬 검증 후 설치·`init` 동작
  - 체크섬 파일을 1바이트 변조하면 cosign 검증이 거부함
- QUICKSTART 설치를 바이너리 기준으로 변경. 저장소 없이도 솔버 매니페스트와 예제를 받는 명령 추가. 소스 빌드는 접이식으로 남김

---

## 2026-10-02 — 첫 공개 부트스트랩 노드, 인터넷 구간 실측

### 배포
- AWS Lightsail 싱가포르(ap-southeast-1), Ubuntu 24.04, 2 vCPU / 0.9 GB, 고정 IP `52.77.24.240`
- Peer ID `12D3KooWDDtqx4Wx1n4FruMVgNVj2J7UkQUiBZU3FDAiyVL59EcA`
- 설치 스크립트로 설치, systemd `pumat-agent`(사용자 `pumat`), `dhtMode: server`, `relayService: true`
- 관리 SSH 키는 이 Mac의 `~/.ssh/pumat_bootstrap`

### 실측하며 발견해 고친 문제
1. **Lightsail 방화벽**: 4001이 막혀 있었음 → 사용자가 IPv4/IPv6 규칙 추가
2. **공인 IP 미광고**: AWS 1:1 NAT라 노드가 사설 IP(172.26.x)만 앎 → `network.announce` 추가
3. **중계 서비스 미기동**: go-libp2p는 AutoNAT로 Public이 확인돼야 relay를 켜는데, 노드가 적으면 계속 Unknown → `network.reachability: public` 추가
4. **중계 위 스트림 거부 + 전송량 제한**: libp2p는 제한 연결 위 스트림을 기본 거부하고, Relay v2는 연결당 바이트가 제한됨 → 스트림을 열 때 hole punching으로 직접 연결이 생기길 최대 20초 기다리고, 제어 메시지(capability·lease·공지)만 중계 허용, 데이터(입력·결과·레코드)는 직접 연결에서만
5. **재시작하면 paused로 바뀜**: 에이전트가 종료 시 paused를 저장했음 → 종료 시에는 메모리에서만 수락 중지, 저장된 모드 유지
6. 테스트와 e2e 스크립트가 실제 부트스트랩에 접속하지 않도록 분리

### 인터넷 구간 E2E 결과
- 요청자: 싱가포르 AWS (인바운드 4002 차단 상태)
- 워커: 한국, 공유기 NAT 뒤 Mac (Docker로 실제 QE 실행), 포트 개방 없음
- `--peer` 없이 부트스트랩 DHT에서 워커 발견 → 중계 연결 → **hole punching 성공으로 직접 연결** → 계약·업로드·실행·결과 회수 완료, **12.8초**, 에너지 −310.569142 eV

### 기본 설정 변경
- `config.DefaultBootstrap`과 기본 `staticRelays`에 1호기 등록 → 새 노드는 설정 없이 인터넷 너머 워커를 찾음
