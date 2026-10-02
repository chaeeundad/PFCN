# Pumat 개발 이력 (DEVLOG)

중요한 결정, 구현 내역, 사용자 조치 필요 사항을 시간순으로 남깁니다.
설계 결정의 근거는 [docs/adr/](adr/)에, 프로토콜 정의는 [architecture.md](architecture.md)에 있습니다.

---

## 사용자 조치가 필요한 항목 (열린 목록)

| # | 항목 | 이유 | 상태 |
|---|---|---|---|
| U1 | `secrets/solver-signing.key` 오프라인 백업 | 프로젝트 솔버 서명 키(신뢰 루트). 분실 시 기존 매니페스트를 재서명해야 하고, 유출 시 임의 이미지가 승인될 수 있음 (ADR-0009) | 대기 |
| U2 | `.env`에 `GHCR_USERNAME`/`GHCR_TOKEN` 입력 (write:packages) | QE 솔버 이미지를 GHCR에 공개해야 다른 머신이 digest로 pull 가능 | 대기 |
| U3 | Linux 머신 2대 이상에서 실제 다중 노드 테스트 | 현재 검증은 macOS(OrbStack) 한 대에서 두 노드. 서로 다른 네트워크 간 연결은 Phase 2 이후 | 대기 |
| U4 | 라이선스 결정 (스펙 권장: 코드 Apache-2.0, 스펙 CC BY 4.0) | LICENSE 파일이 없으면 외부 기여·사용이 법적으로 불명확 | 대기 |
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
