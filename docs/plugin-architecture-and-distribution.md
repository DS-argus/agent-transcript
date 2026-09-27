# Agent Transcript: tmux 플러그인 구조·배포

이 문서는 Agent Transcript를 **tmux 플러그인**으로 로드·구성·빌드하는 방법과 내부 처리 경계를 설명한다. 사용자가 누르는 표면은 tmux key binding이고, Go 실행 파일은 플러그인이 호출하는 내부 engine이다.

## 1. 구성 요소와 entrypoint

플러그인은 tmux 안에 공유 library를 로드하지 않는다. 실행 가능한 shell entrypoint, tmux 설정, 플러그인 디렉터리에 준비된 Go 실행 파일, 사용자가 설치한 Markdown reader가 함께 동작한다.

```text
plugin-repo/
├─ agent-transcript.tmux       # TPM/tpack이 실행할 shell entrypoint
├─ tmux/agent-transcript.tmux  # tmux 기본값·key binding
├─ bin/agent-transcript        # 현재 host용 Go 실행 파일
├─ cmd/                        # Go engine source
├─ internal/                   # agent 식별·parser·reader 구현
└─ README.md
```

두 `.tmux` 파일은 서로 대체할 수 없다.

```text
plugin manager 또는 tmux run-shell
  → agent-transcript.tmux가 자신의 위치를 계산하고 bin/agent-transcript 확인
  → tmux/agent-transcript.tmux를 source-file
  → prefix + P / copy-mode P 등록
```

TPM/tpack에서 `set -g @plugin 'DS-argus/agent-transcript'`를 선언한다. 설치와 재로드 시 플랫폼별 바이너리가 자동 준비된다. 로컬 source build도 다음처럼 사용할 수 있다.

```sh
make build
tmux run-shell '/path/to/agent-transcript/agent-transcript.tmux'
```

바이너리가 준비된 뒤에는 설정 파일을 직접 source할 수도 있다.

```sh
tmux source-file /path/to/agent-transcript/tmux/agent-transcript.tmux
```

shell entrypoint는 `scripts/install.sh`를 실행하여 VERSION에 고정된 GitHub Release 바이너리를 준비한 뒤 설정을 로드한다. 설치에는 curl 또는 wget, sha256sum 또는 shasum이 필요하며 reader는 사용자가 별도로 설치한다. 바이너리는 bin/의 임시 경로에서 검증한 뒤 교체하며 동시 설치는 잠금으로 직렬화한다.

## 2. Plugin manager와 설정

[TPM](https://github.com/tmux-plugins/tpm)과 [tpack](https://github.com/tmuxpack/tpack)은 저장소의 root `*.tmux` entrypoint를 실행하는 manager다. 각 manager의 설치·초기화 문법과 plugin 경로는 해당 공식 문서를 따른다. 두 manager를 같은 plugin root에 동시에 초기화하지 않는다.

`agent-transcript.tmux`는 고정 설치 경로를 가정하지 않고 자신의 위치에서 `bin/agent-transcript`를 찾는다. 따라서 manager가 checkout을 어느 plugin 디렉터리에 두더라도 내부 경로가 유지된다. manager가 source를 설치하는 것과 Go 실행 파일·reader를 준비하는 것은 별개다.

### 2.1 Plugin 옵션

`.tmux.conf`에서 값을 선언한 뒤 plugin 설정을 source한다.

```tmux
set -g @agent_transcript_reader 'leaf'     # leaf | glow
set -g @agent_transcript_position 'right'  # right | left | top | bottom
set -g @agent_transcript_size '50%'        # 1..99%, 또는 양의 cell 수
set -g @agent_transcript_focus 'on'        # on | off
set -g @agent_transcript_key 'P'
set -g @agent_transcript_copy_mode_key 'P'
source-file /path/to/agent-transcript/tmux/agent-transcript.tmux
```

기본값은 기존 사용자 option을 덮어쓰지 않는다. reader·방향·크기·focus는 key를 누를 때 읽고, key를 바꾼 뒤에는 설정 파일을 다시 source해야 이전 plugin 소유 binding을 정리할 수 있다. `none`으로 개별 key를 끌 수 있으며 잘못된 값과 key 충돌은 오류다.

`prefix + P` 또는 copy-mode의 `P`는 대상 pane의 최신 대화를 reader pane에서 연다. 같은 source/session viewer가 있으면 새 snapshot으로 그 pane을 갱신한다. 새 pane은 source agent pane을 기준으로 나누고 focus는 option을 따른다. 갱신은 맨 아래에서 시작하며 자동 반복 갱신은 없다. 방향·크기는 새 pane에만 적용된다.

## 3. Build 산출물

[`Makefile`](../Makefile)의 공개 개발 명령은 세 가지다.

| 명령 | 동작 |
|---|---|
| `make build` | 현재 OS·CPU용 `bin/agent-transcript` 생성 |
| `make test` | `go test ./...` 실행 |
| `make release VERSION=0.1.0` | Darwin/Linux·arm64/amd64 네 산출물을 `dist/`에 생성 |

```text
dist/
├─ agent-transcript-darwin-arm64
├─ agent-transcript-darwin-amd64
├─ agent-transcript-linux-arm64
└─ agent-transcript-linux-amd64
```

`make build`는 임시 파일을 성공한 결과로 교체한다. 기본 VERSION은 루트 VERSION 파일에서 읽으며 `--version`에 삽입된다. `make release`는 네 플랫폼을 교차 빌드하고 정확히 그 네 파일의 `checksums.txt`를 생성한다. `.github/workflows/release.yml`은 `v*` tag에서 tag와 VERSION을 검증하고 Release asset을 게시한다.

## 4. 지원 범위와 외부 의존성

- **실행 환경:** macOS/Linux, arm64/amd64. Windows native는 대상이 아니다.
- **실행 도구:** tmux 3.2 이상, `ps`, `lsof`, 그리고 [Leaf](https://github.com/RivoLink/leaf)(기본) 또는 [Glow](https://github.com/charmbracelet/glow)(대안).
- **소스 build:** Go 1.24 이상과 Make.
- Go 바이너리는 `CGO_ENABLED=0`으로 build하지만 OS process API와 위 외부 도구 의존성은 없어지지 않는다.
- GJC 자동 process identity·session discovery는 macOS 구현만 제공하며, 유지되는 registry snapshot+journal 파일을 offline replay한다. 런타임에 `gjc sdk`를 호출하거나 broker에 연결하지 않는다. Linux에서 GJC는 지원하지 않으며 다른 수동 파일 선택 경로를 제공하지 않는다.
- 교차 build 성공은 각 OS/CPU에서의 실제 동작 검증을 뜻하지 않는다.

reader는 native Markdown TUI로 실행된다. Leaf가 기본이고 Glow를 대안으로 선택할 수 있다. 프로그램이 없으면 다른 reader로 대체하지 않는다. Glow에는 `GLOW_PAGER=false`를 전달한다.

## 5. 내부 처리 engine

### 5.1 Foreground-first pipeline

```text
prefix + P
  → 대상 pane 결정 (기본값: TMUX_PANE)
  → foreground process 검사
  ├─ Codex/GJC 인식
  │    → 선택된 agent의 소유 session만 조회
  │    → 저장 기록 parser 실행
  │    → 공통 Markdown snapshot 생성
  │    → pane 분할·reader 실행
  └─ 지원하지 않는 app 또는 shell
       → pane을 나누기 전에 tmux screen/history 캡처
       → Markdown reader 실행
```

자동 선택은 가장 앞의 foreground 응용프로그램만 신뢰한다. 하위 tool, MCP server, nested agent, background job을 별도 session 후보로 탐색하지 않는다.

화면 캡처 fallback은 **지원하지 않는 foreground app 또는 shell**에만 적용한다. process inspection 실패, 인식된 agent의 session 조회 실패, 저장 기록의 손상·모호성·parser 실패는 원래 오류를 반환하며 screen으로 바꾸지 않는다. 인식된 agent의 transcript가 없거나 읽을 수 없는 경우에도 같은 규칙을 따른다.

### 5.2 Source layout

```text
cmd/agent-transcript/
├─ main.go                    # 공개 인자와 내부 subcommand 진입
├─ configuration.go           # tmux option 검증·managed binding
├─ lifecycle.go               # viewer 열기·갱신·시작 대기·실패 정리
├─ pane.go                    # pane 조회·소유 관계·source별 잠금
├─ snapshot.go                # 세션 식별·Markdown snapshot 생성·정리
└─ startup.go                 # reader 준비·하단 이동·focus
internal/
├─ harness/
│  ├─ registry.go             # 인식 가능한 agent adapter 목록
│  ├─ foreground.go           # foreground process 식별
│  ├─ resolver.go             # 식별 후 해당 agent session 조회
│  ├─ files.go                # owner process의 열린 파일 조회
│  ├─ codex.go / claude.go / gjc.go
│  └─ identity_*.go            # process identity 구현·플랫폼 제한
├─ transcript/
│  ├─ document.go             # bounded JSONL과 Markdown framing
│  ├─ codex.go / claude.go / gjc.go
│  └─ *_test.go                # schema·graph·filter test
└─ reader/reader.go            # Leaf/Glow 실행 계약
```

각 agent adapter는 foreground 식별 규칙, owner process에서 session을 찾는 locator, 저장 형식 감지, 공개 message Markdown 변환을 함께 정의한다. resolver는 foreground가 결정된 뒤 해당 adapter의 locator만 호출하므로 무관한 agent 저장소를 열어 보지 않는다.

### 5.3 Agent 기록과 native directory option

- **Codex:** 식별된 owner가 연 CLI rollout들의 첫 행만 읽는다. 여러 후보가 `forked_from_id`로 하나의 순환 없는 부모 체인을 이루면 유일한 말단 fork를 선택한다. alias 경로는 중복 제거하고 중복 session ID·잘못된 부모·순환은 거부한다. 무관한 세션·형제 fork는 계속 모호성 오류이며 수정 시각·FD 번호·`history_base`로 선택하지 않는다. 선택 파일에 저장된 완료 user/assistant event만 표시하고 상속된 원본 history는 읽지 않는다. 빈 fork는 저장된 메시지가 없다는 안내를 낸다. 이는 TUI active thread 동기화가 아니라 fork 우선 표시 정책이라 원본으로 resume했어도 자손이 열린 채면 자손을 선택할 수 있다. `/clear` 직후 새 rollout 저장 전의 이전 대화 표시는 유지하며, clear 후 무관한 파일이 둘 다 남는 경우는 여전히 오류다. tool call, reasoning, worker thread는 제외한다.
- **Claude Code (첫 버전 미지원, 코드 보존):** `uuid`/`parentUuid` 관계를 따라 user·assistant 본문을 복원하고 system, sidechain, tool, thinking 기록은 제외한다. config root는 `CLAUDE_CONFIG_DIR` 환경 변수를 사용하며 기본값은 `~/.claude`다.
  Claude adapter는 `internal/harness/registry.go`에 등록하지 않는다. 탐색·파싱 구현과 단위 테스트는 보존하지만 기본 플러그인에서는 Claude를 미지원 foreground로 처리하여 screen capture를 사용한다. 구독 계정 없이 정상 응답 검증이 제한되고 fork 기록의 `sessionId`/`session_id` 불일치 처리 문제가 확인되어 재지원 전 별도 검증이 필요하다.
- **GJC:** `id`/`parentId` 관계를 따라 공개 user·assistant text를 복원하고 tool data와 thinking을 제외한다. agent root는 `GJC_CODING_AGENT_DIR`, 그 다음 `GJC_CONFIG_DIR` 또는 `PI_CONFIG_DIR`을 사용하고 기본값은 `~/.gjc/agent`다. 자동 identity 검증은 macOS에서만 가능하다.

GJC는 마지막으로 저장된 graph record에서 `parentId`를 따라 부모를 수집한 뒤 시간 순서로 표시한다. `header_patch`는 graph에서 제외하고, 파일 전체의 leaf 개수가 하나일 필요는 없다. 선택 경로 밖의 sibling branch는 합치지 않는다. 부모가 없는 `customType: context_clear` record를 만나면 파일 순서상 직전 graph record의 부모 체인으로 연결하고 **Context cleared** 안내 블록을 넣는다. 일반적인 parentless record에서는 탐색을 끝낸다. 이 연결은 과거 대화 열람을 위한 표시 정책이며, clear marker에 이전 active leaf가 저장되지 않으므로 clear 직전 UI의 분기 선택을 정확히 복원한다고 보장하지 않는다. 저장 없는 UI 분기 이동도 추적하지 않는다. `/fork`는 별도 session/file로서 기존 session 탐색이 처리한다. Codex·Claude의 parser와 공통 JSONL reader에는 이 정책을 적용하지 않는다.

GJC foreground discovery는 `$GJC_CODING_AGENT_DIR/sdk/sessions/index.snapshot.json` (v4)와 `index.jsonl`을 직접 읽어 snapshot+journal을 offline replay한다. `GJC_CODING_AGENT_DIR`의 환경 변수 fallback은 그대로 사용한다. Replay는 순서·checksum을 검증하고 파일 stamp/inode가 안정될 때까지 bounded retry하며, snapshot 교체 중에는 검증된 pre-snapshot tail prefix만 허용한다. 런타임에 `gjc sdk`를 호출하지 않고 GJC SDK broker에도 연결하지 않는다. 이 registry 파일이 유지되어 있어야 하며, 플러그인이 broker를 시작·재시작하지 않는다.

Replay 결과에서 foreground PID와 OS process incarnation이 일치하는 후보만 사용한다. authority/root를 먼저 고정한 뒤 lifecycle 상태와 heartbeat를 분리해 liveness를 판단하고, tombstone·terminal uncertainty·신선도 부족은 fail-closed 오류로 처리한다. 같은 identity의 실제 endpoint가 확인된 경우에만 그 프로세스의 generation 0 direct-bookkeeping 등록을 제외하며, generation 0을 일괄 삭제하지 않는다. 선택한 JSONL의 header를 확인한 뒤 registry를 다시 replay/reselect하고 허용된 모든 PID의 process identity를 재확인한다. heartbeat 또는 indexSeq만 바뀐 경우는 허용하지만 authority/session 또는 PID identity 전환은 오류다. 대화 본문은 기존 JSONL parser로 읽고, 메모리의 `agent.state.messages`를 가져오는 것은 아니다.

이 환경 변수들은 각 agent가 제공하는 native storage 위치를 지정한다. command-line 경로 override나 사용자가 고르는 transcript/branch/session mode는 제공하지 않는다. 고정 snapshot은 파일을 연 시점의 bytes만 읽는다. 마지막 불완전 JSONL record는 안내와 함께 제외하고, 완성 record가 손상되면 오류다. 원본 agent 로그와 사용자 reader 설정은 수정하지 않는다.

### 5.4 실행 인자와 내부 subcommand

plugin이 호출하는 공개 실행 인자는 다음으로 제한된다.

- `--reader leaf|glow`
- `--position right|left|top|bottom`
- `--size SIZE`
- `--focus on|off`
- `--notify-client CLIENT`
- 선택적인 target pane
- `--help`/`-h`, `--version`

`_configure`는 `tmux/agent-transcript.tmux`가 호출하여 managed binding을 검증·등록한다. `_view`는 Go lifecycle이 viewer pane을 생성하거나 교체할 때 spawn하는 내부 단계이며 shell entrypoint가 호출하지 않는다. 두 subcommand는 사용자에게 session 파일·branch·agent 종류·저장 경로를 고르는 interface를 제공하지 않는다.

### 5.5 Snapshot·reader 수명

```text
macOS: ~/Library/Caches/agent-transcript/
Linux: $XDG_CACHE_HOME/agent-transcript/ 또는 ~/.cache/agent-transcript/

agent-transcript/
├─ snapshots/snapshot-<random>/snapshot.md
└─ locks/<server-source-hash>.lock
```

cache root와 하위 directory는 0700, snapshot 문서는 0600이다. `_view`는 reader가 descriptor를 연 뒤 이름을 unlink하고 reader process로 대체된다. 정상 종료와 실패 시 작업 directory를 정리하지만 강제 종료 잔여물은 남을 수 있다. lock file은 빈 파일로 유지되며 실행 중 삭제하면 안 된다.

reader 초기화가 실제 process·raw terminal·alternate screen·문서 rendering을 확인한 뒤 하단 이동 key를 한 번만 보낸다. 실행 실패는 직접 호출에서는 stderr/nonzero, plugin key에서는 호출 client의 짧은 status notice로 전달된다. 성공 시 plugin은 별도 output overlay를 만들지 않는다.

## 6. 배포 상태와 참고 자료

배포 진입점은 `scripts/install.sh`로 checkout의 VERSION과 일치하는 바이너리를 확인한다. 없거나 다른 버전이면 GitHub Release의 해당 OS·CPU 파일과 checksum을 받아 검증 후 원자적으로 설치한다. 버전이 맞으면 네트워크 조회 없이 기존 바이너리를 사용한다. 실패한 설치는 기존 파일을 보존하고 plugin 로드를 중단한다. 자동 reader 설치와 바이너리 서명은 제공하지 않는다.

- [TPM 공식 저장소](https://github.com/tmux-plugins/tpm)
- [TPM 플러그인 제작 가이드](https://github.com/tmux-plugins/tpm/blob/master/docs/how_to_create_plugin.md)
- [tpack 공식 저장소](https://github.com/tmuxpack/tpack)
- [tpack 설치 문서](https://tmuxpack.github.io/tpack/getting-started/installation/)
- [프로젝트 README](../README.md)
- [빌드 규칙](../Makefile)
- [Go engine 진입점](../cmd/agent-transcript/main.go)

## 7. 첫 배포의 commit·ignore 경계

### 소스 commit에 포함

- `.gitignore`, `README.md`, `Makefile`, `VERSION`, `go.mod`, `go.sum`, `.github/workflows/release.yml`
- `scripts/install.sh`, `scripts/install_test.sh`
- `agent-transcript.tmux` (실행 권한 100755), `tmux/agent-transcript.tmux`
- `cmd/agent-transcript/`의 모든 Go source와 `_test.go`
- `internal/`의 모든 Go source와 `_test.go`: 비활성 Claude 구현·테스트도 보존
- 이 문서 `docs/plugin-architecture-and-distribution.md`

### 로컬에 보존하되 commit하지 않음

- `bin/`, `dist/`: 생성한 실행 파일·배포 산출물
- `.gjc/`, `.omc/`, `.omx/`: agent 상태, 대화/작업 기록, 인증 정보를 포함할 수 있는 endpoint metadata
- `docs/agent-discovery-investigation.md`, `docs/note.md`: 로컬 조사 이력·실제 세션 ID·개인 메모
- `.DS_Store`, `.env*` (의도적으로 제공하는 `.env.example` 제외), 진단용 `.*-check.*/`, `*.test`, coverage 결과

이 경계는 루트 `.gitignore`에 반영한다. ignore는 이미 추적 중인 파일을 자동으로 제거하거나 비밀을 지우는 기능이 아니므로, 기존 Git 저장소에 적용할 때는 staged diff도 확인해야 한다. 조사 문서와 사용자 상태 파일은 삭제하지 않는다. 공용 README에서 로컬 전용 문서로 링크하지 않는다.

### Release 첨부물

Git에는 source를 저장하고 OS·CPU별 바이너리는 release asset으로 따로 게시한다. 배포 버전이 결정되면 같은 값으로 빌드한다.

```sh
make test
go vet ./...
go test -race ./...
sh scripts/install_test.sh
# VERSION과 release tag가 일치해야 함
make release VERSION=0.1.0
```

`dist/agent-transcript-{darwin,linux}-{arm64,amd64}` 네 파일과 `dist/checksums.txt`를 게시한다. `VERSION`에 기록된 버전으로 빌드하고 tag는 `v<VERSION>`을 사용한다. GitHub Actions release workflow는 tag/version 일치를 검증한 뒤 테스트·교차 빌드·checksum 생성·Release 업로드를 수행한다. 바이너리 서명은 제공하지 않으며 SHA-256 manifest는 같은 HTTPS Release에서 받는 무결성 검사다.

### 게시 전 확인할 결정 사항

- 저장소 `DS-argus/agent-transcript`, 초기 버전 `0.1.0` / tag `v0.1.0`; 이후 VERSION과 tag 일치 유지
- 공개 배포 라이선스 결정: 현재 LICENSE 파일이 없으며 자동으로 임의의 라이선스를 부여하지 않음
- README의 Codex 0.154.0 실측 범위, 0.157.1 shared daemon 미검증, Linux runtime 미검증, GJC macOS 제한을 그대로 공지
- Claude는 구조화된 대화 지원에서 제외하지만 미지원 프로그램의 screen fallback은 유지

저장소는 `https://github.com/DS-argus/agent-transcript`를 사용한다. release tag 게시 전 source/test/installer 검증과 staged diff 점검을 수행한다. 공개 저장소와 Release asset이 모두 접근 가능해야 인증 없는 TPM/tpack 설치가 완료된다.
