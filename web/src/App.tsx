import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

type Repo = { id: number; owner: string; name: string; private: boolean; tombstone_count?: number; high_confidence?: number; unknown_reason?: number };
type Claim = { claim: string; confidence: number; evidence_ids: string[] };
type Evidence = { id: string; type: string; author: string; body: string; source_url: string; rank_score: number; path?: string; line?: number };
type Components = { semantic: number | null; title_body: number; files: number; paths: number; approach: number; labels: number; symbols: number };
type Match = { id: number; new_pr_number: number; old_pr_number: number; score: number; relationship: string; reason: string; components?: Components };
type Tombstone = {
  id: number;
  repository: Repo;
  pull_request: { number: number; title: string; author: string; files: { filename: string }[] };
  state: string;
  summary: string;
  outcomes: string[];
  attempted_approach: Claim[];
  valuable_findings: Claim[];
  rejected_or_questioned_approaches: Claim[];
  unresolved_questions: Claim[];
  suggested_future_direction: Claim[];
  affected_areas: string[];
  evidence: Evidence[];
  confidence: number;
};
type Settings = { repository_id: number; notify_mode: "dashboard" | "check"; retention_days: number; contents_enabled: boolean };
type Graph = { nodes: { id: string; type: string; label: string }[] | null; edges: { id: number; source: string; target: string; relation: string }[] | null };
type History = { number: number; title: string; author: string; matches: Match[] | null };
type JobStats = { pending: number; running: number; completed: number; failed: number };
type Tab = "tombstones" | "history" | "graph" | "settings";
type AuthUser = { id: number; github_id: number; login: string; name: string; avatar_url: string };
type AuthStatus = { mode: "oauth" | "token" | "open"; user: AuthUser | null };

function authHeaders(token: string, json = false): HeadersInit {
  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  if (json) headers["Content-Type"] = "application/json";
  return headers;
}

async function apiJSON<T>(url: string, token: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(url, { ...init, headers: { ...authHeaders(token, !!init.body), ...init.headers } });
  if (!response.ok) throw new Error((await response.text()).trim() || `${response.status} ${response.statusText}`);
  return response.json();
}

const tabMeta: Record<Tab, { label: string; icon: string }> = {
  tombstones: { label: "Tombstones", icon: "♙" },
  history: { label: "New PR History", icon: "◴" },
  graph: { label: "Graph", icon: "▣" },
  settings: { label: "Settings", icon: "⚙" },
};

export default function App() {
  const queryClient = useQueryClient();
  const [token, setToken] = useState("");
  const [tokenDraft, setTokenDraft] = useState("");
  const [activeRepoID, setActiveRepoID] = useState<number | null>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const [query, setQuery] = useState("");
  const [listLimit, setListLimit] = useState(100);
  const [tab, setTab] = useState<Tab>("tombstones");

  const auth = useQuery({ queryKey: ["auth"], queryFn: () => apiJSON<AuthStatus>("/api/auth/me", ""), retry: false, refetchOnWindowFocus: true });
  const authMode = auth.data?.mode;
  const authed = auth.data ? (auth.data.mode === "oauth" ? !!auth.data.user : auth.data.mode === "token" ? !!token : true) : false;
  const logout = useMutation({ mutationFn: () => apiJSON<{ ok: boolean }>("/api/auth/logout", "", { method: "POST" }), onSuccess: () => { setActiveRepoID(null); setSelected(null); queryClient.invalidateQueries({ queryKey: ["auth"] }); } });

  const repos = useQuery({ queryKey: ["repositories", token], queryFn: () => apiJSON<{ repositories: Repo[] | null }>("/api/repositories", token), enabled: authed });
  const repositoryList = repos.data?.repositories || [];
  useEffect(() => {
    if (repositoryList.length && !repositoryList.some((repo) => repo.id === activeRepoID)) setActiveRepoID(repositoryList[0].id);
  }, [repos.data, activeRepoID]);

  const activeRepo = useMemo(() => repositoryList.find((repo) => repo.id === activeRepoID), [repos.data, activeRepoID]);
  const tombstones = useQuery({
    queryKey: ["tombstones", activeRepo?.id, query, listLimit, token], enabled: !!activeRepo && tab === "tombstones",
    queryFn: () => apiJSON<{ tombstones: Tombstone[] | null; has_more: boolean }>(`/api/tombstones/repository/${activeRepo!.id}?limit=${listLimit}${query ? `&q=${encodeURIComponent(query)}` : ""}`, token),
  });
  const detail = useQuery({ queryKey: ["tombstone", selected, token], enabled: !!selected && tab === "tombstones", queryFn: () => apiJSON<Tombstone>(`/api/tombstones/${selected}`, token) });
  const related = useQuery({ queryKey: ["related", selected, token], enabled: !!selected && tab === "tombstones", queryFn: () => apiJSON<{ matches: Match[] | null }>(`/api/tombstones/${selected}/related`, token) });
  const graph = useQuery({ queryKey: ["graph", activeRepo?.id, token], enabled: !!activeRepo && tab === "graph", queryFn: () => apiJSON<Graph>(`/api/graph/repository/${activeRepo!.id}`, token) });
  const history = useQuery({ queryKey: ["history", activeRepo?.id, token], enabled: !!activeRepo && tab === "history", queryFn: () => apiJSON<{ pull_requests: History[] | null }>(`/api/repositories/${activeRepo!.id}/history`, token) });
  const settings = useQuery({ queryKey: ["settings", activeRepo?.id, token], enabled: !!activeRepo && tab === "settings", queryFn: () => apiJSON<Settings>(`/api/repositories/${activeRepo!.id}/settings`, token) });
  const jobs = useQuery({ queryKey: ["jobs", token], queryFn: () => apiJSON<JobStats>("/api/jobs", token), refetchInterval: 5000, retry: false, enabled: authed });

  const reanalyze = useMutation({
    mutationFn: (id: number) => apiJSON<{ queued: boolean }>(`/api/tombstones/${id}/reanalyze`, token, { method: "POST" }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["tombstones"] }); queryClient.invalidateQueries({ queryKey: ["jobs"] }); },
  });
  const updateState = useMutation({
    mutationFn: ({ id, state }: { id: number; state: string }) => apiJSON<{ id: number; state: string }>(`/api/tombstones/${id}/state`, token, { method: "PUT", body: JSON.stringify({ state }) }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["tombstone", selected] }); queryClient.invalidateQueries({ queryKey: ["tombstones"] }); },
  });

  function saveToken() {
    setToken(tokenDraft.trim());
    setActiveRepoID(null);
    setSelected(null);
  }

  function selectRepo(id: number) {
    setActiveRepoID(id);
    setSelected(null);
    setQuery("");
    setListLimit(100);
  }

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand-row">
        <div className="brand-mark"><span /></div>
        <strong>PR Tombstone</strong>
        <span className="sidebar-collapse" aria-hidden="true">‹‹</span>
      </div>

      <div className="sidebar-heading"><span>Repositories</span><a href="/api/github/install" className="sidebar-add" aria-label="Install GitHub App">+</a></div>
      <div className="repo-list">
        {repos.isLoading && <p className="sidebar-status">Loading repositories...</p>}
        {repos.isError && <p className="error sidebar-status">{String(repos.error)}</p>}
        {repositoryList.map((repo) => <button className={`repo ${repo.id === activeRepo?.id ? "active" : ""}`} key={repo.id} onClick={() => selectRepo(repo.id)}>
          <span className="repo-icon">&lt;/&gt;</span>
          <span className="repo-copy"><strong>{repo.owner}/{repo.name}</strong><small><span>◉</span> {repo.private ? "Private" : "Public"} · {repo.tombstone_count || 0} Tombstones</small></span>
        </button>)}
        {!repositoryList.length && !repos.isLoading && !repos.isError && <div className="sidebar-empty">No installed repositories yet.<a href="/api/github/install">Install GitHub App →</a></div>}
      </div>

      <div className="sidebar-promo">
        <span className="promo-spark">✦</span>
        <div><strong>Make better merge decisions</strong><p>Understand why PRs were closed so you can ship with confidence.</p><a href="https://github.com/TomorrowX6/PR-Tombstone" target="_blank" rel="noreferrer">Learn more →</a></div>
      </div>
    </aside>

    <div className="workspace">
      <div className="topbar">
        <div className="queue" title="Analysis queue">
          <span><i className="queue-dot pending" />{jobs.data?.pending || 0} queued</span>
          <span><i className="queue-dot running" />{jobs.data?.running || 0} running</span>
        </div>
        <div className="topbar-actions">
          {authMode === "oauth" && (auth.data?.user ? <>
            <div className="account"><img src={auth.data.user.avatar_url} alt="" referrerPolicy="no-referrer" /><span>{auth.data.user.name || auth.data.user.login}</span><b>⌄</b></div>
            <button className="signout" onClick={() => logout.mutate()} disabled={logout.isPending}>{logout.isPending ? "Signing out..." : "Sign out"}</button>
          </> : <a className="signin" href="/api/auth/login">Sign in with GitHub</a>)}
          {authMode === "token" && <div className="token-box"><input type="password" value={tokenDraft} onChange={(event) => setTokenDraft(event.target.value)} onKeyDown={(event) => event.key === "Enter" && saveToken()} placeholder="Dashboard token" /><button onClick={saveToken}>Apply</button></div>}
          <span className="read-only"><span>♢</span> Read-only by default</span>
        </div>
      </div>

      <section className="dashboard">
        <header className="hero">
          <p className="eyebrow">Repository decision memory</p>
          <h1>PR Tombstone</h1>
          <p className="tagline">Dead patches still have something to teach us.</p>
        </header>

        <section className="content">
          <div className="section-head">
            <div>
              <div className="repo-context"><span className="repo-context-icon">▤</span><strong>{activeRepo ? `${activeRepo.owner}/${activeRepo.name}` : "No repository"}</strong>{activeRepo && <span className="visibility-badge">{activeRepo.private ? "Private" : "Public"}</span>}</div>
              <h2>Decision memory</h2>
            </div>
            {tab === "tombstones" && <label className="search-box"><span>⌕</span><input value={query} onChange={(event) => { setQuery(event.target.value); setListLimit(100); }} placeholder="Search mutex, Vulkan, lifetime..." /><kbd>⌘ K</kbd></label>}
          </div>

          <nav className="tabs">{(Object.keys(tabMeta) as Tab[]).map((value) => <button key={value} className={tab === value ? "active" : ""} onClick={() => setTab(value)}><span>{tabMeta[value].icon}</span>{tabMeta[value].label}</button>)}</nav>

          {activeRepo && <div className="repo-stats">
            <StatCard tone="purple" icon="♙" value={activeRepo.tombstone_count || 0} label="Tombstones" description="Total preserved attempts" />
            <StatCard tone="blue" icon="♢" value={activeRepo.high_confidence || 0} label="High confidence" description="Strong rejection signals" />
            <StatCard tone="cyan" icon="?" value={activeRepo.unknown_reason || 0} label="Unknown reason" description="Unclear or missing context" />
          </div>}

          {!activeRepo && <div className="empty page-empty">{authMode === "oauth" && !auth.data?.user ? <><p>Sign in with GitHub to view your repositories.</p><a className="install-link" href="/api/auth/login">Sign in with GitHub →</a></> : <><p>Select or install a repository to continue.</p><a className="install-link" href="/api/github/install">Install GitHub App →</a></>}</div>}
          {activeRepo && tab === "tombstones" && <TombstoneView query={query} clearQuery={() => setQuery("")} tombstones={tombstones.data?.tombstones || []} loading={tombstones.isLoading} hasMore={!!tombstones.data?.has_more} loadMore={() => setListLimit((value) => Math.min(3000, value + 100))} selected={selected} setSelected={setSelected} detail={detail.data} detailLoading={detail.isLoading} related={related.data?.matches || []} reanalyze={() => detail.data && reanalyze.mutate(detail.data.id)} reanalyzing={reanalyze.isPending} updateState={(state) => detail.data && updateState.mutate({ id: detail.data.id, state })} stateUpdating={updateState.isPending} />}
          {activeRepo && tab === "history" && <HistoryView items={history.data?.pull_requests || []} loading={history.isLoading} />}
          {activeRepo && tab === "graph" && <GraphView graph={graph.data} loading={graph.isLoading} />}
          {activeRepo && tab === "settings" && <SettingsView repo={activeRepo} value={settings.data} loading={settings.isLoading} token={token} queryClient={queryClient} />}
        </section>
      </section>
    </div>
  </div>;
}

function StatCard({ tone, icon, value, label, description }: { tone: string; icon: string; value: number; label: string; description: string }) {
  return <div className={`stat-card ${tone}`}><span className="stat-icon">{icon}</span><div><div className="stat-value"><strong>{value}</strong><span>{label}</span></div><p>{description}</p></div></div>;
}

function TombstoneView(props: { query: string; clearQuery: () => void; tombstones: Tombstone[]; loading: boolean; hasMore: boolean; loadMore: () => void; selected: number | null; setSelected: (id: number) => void; detail?: Tombstone; detailLoading: boolean; related: Match[]; reanalyze: () => void; reanalyzing: boolean; updateState: (state: string) => void; stateUpdating: boolean }) {
  const evidence = props.detail?.evidence || [];
  return <div className="layout">
    <div className="list">
      {props.loading && <div className="empty list-empty"><span className="empty-search">⌕</span><strong>Loading Tombstones...</strong><p>Looking through preserved repository history.</p></div>}
      {props.tombstones.map((item) => <button className={`card ${item.id === props.selected ? "selected" : ""}`} key={item.id} onClick={() => props.setSelected(item.id)}><div className="card-top"><strong>PR #{item.pull_request.number}</strong><span className={`state ${item.state.toLowerCase()}`}>{item.state}</span></div><h3>{item.pull_request.title || "Untitled pull request"}</h3><p>{item.summary || "No summary available."}</p><div className="card-bottom"><span>{item.outcomes?.join(" · ") || "unknown"}</span><span>{Math.round(item.confidence * 100)}%</span></div></button>)}
      {props.hasMore && <button className="load-more" onClick={props.loadMore}>Load more</button>}
      {!props.tombstones.length && !props.loading && <div className="empty list-empty"><span className="empty-search">⌕</span><strong>{props.query ? "No results for your search" : "No Tombstones yet"}</strong><p>{props.query ? "No preserved attempts match your current search or filters." : "Closed, unmerged PRs will appear here after they are analyzed."}</p>{props.query && <button className="clear-search" onClick={props.clearQuery}>Clear search <span>×</span></button>}</div>}
    </div>

    <article className="detail">
      {props.detailLoading && <p>Loading...</p>}
      {props.detail && <><div className="detail-top"><div><p className="eyebrow">PR #{props.detail.pull_request.number} · {props.detail.pull_request.author}</p><h2>{props.detail.pull_request.title}</h2></div><div className="detail-actions"><select aria-label="Tombstone state" value={props.detail.state} disabled={props.stateUpdating || props.detail.state === "ARCHIVED_AS_MERGED"} onChange={(event) => props.updateState(event.target.value)}><option value="ACTIVE">Active</option><option value="SUSPENDED">Suspended</option><option value="SUPERSEDED">Superseded</option><option value="INVALIDATED">Invalidated</option><option value="ARCHIVED">Archived</option>{props.detail.state === "ARCHIVED_AS_MERGED" && <option value="ARCHIVED_AS_MERGED">Archived as merged</option>}</select><button className="secondary" onClick={props.reanalyze}>{props.reanalyzing ? "Queued..." : "Re-analyze"}</button></div></div><div className="confidence"><span>Platform confidence</span><strong>{Math.round(props.detail.confidence * 100)}%</strong><i><b style={{ width: `${props.detail.confidence * 100}%` }} /></i></div>
        <DetailSection label="Outcome"><p className="large">{props.detail.outcomes?.join(" · ") || "unknown"}</p></DetailSection><ClaimList label="Attempted approach" claims={props.detail.attempted_approach} evidence={evidence} fallback={props.detail.summary} /><ClaimList label="Why it wasn't merged" claims={props.detail.rejected_or_questioned_approaches} evidence={evidence} fallback="Unknown — the available evidence does not establish a reason." /><ClaimList label="Useful findings" claims={props.detail.valuable_findings} evidence={evidence} /><ClaimList label="Still unresolved" claims={props.detail.unresolved_questions} evidence={evidence} /><ClaimList label="Suggested future direction" claims={props.detail.suggested_future_direction} evidence={evidence} />
        <DetailSection label="Affected areas"><div className="chips">{props.detail.affected_areas?.map((area) => <span key={area}>{area}</span>)}</div></DetailSection><DetailSection label="Related history">{props.related.length ? <div className="related">{props.related.map((match) => <div className="related-item" key={match.id}><strong>PR #{match.old_pr_number}</strong><span>{Math.round(match.score * 100)}% · {match.relationship}</span><MatchComponents components={match.components} /></div>)}</div> : <p className="muted">No strong historical conflicts.</p>}</DetailSection><DetailSection label="Evidence"><div className="evidence">{evidence.map((item) => <a className="evidence-item" key={item.id} href={item.source_url || "#"} target="_blank" rel="noreferrer"><span>{item.type}</span><strong>{item.author || "unknown"}</strong><p>{item.body || item.path || item.id}</p></a>)}</div></DetailSection></>}
      {!props.detail && !props.detailLoading && <div className="detail-empty"><TombstoneArt /><h3>Select a Tombstone to inspect its evidence.</h3><p>Review the context, reason, and signals behind closed PRs.</p></div>}
    </article>
  </div>;
}

function TombstoneArt() {
  return <div className="tombstone-art" aria-hidden="true"><span className="ghost ghost-a">••</span><span className="ghost ghost-b">••</span><div className="stone"><span>&lt;/&gt;</span></div><i className="plant plant-a">✦</i><i className="plant plant-b">✦</i></div>;
}

function HistoryView({ items, loading }: { items: History[]; loading: boolean }) {
  if (loading) return <p>Loading new PR history...</p>;
  if (!items.length) return <div className="empty page-empty">No opened or synchronized pull requests have been analyzed yet.</div>;
  return <div className="history-grid">{items.map((item) => { const matches = item.matches || []; return <article className="history-card" key={item.number}><p className="eyebrow">PR #{item.number} · {item.author}</p><h3>{item.title}</h3>{matches.length ? matches.map((match) => <div className={`match ${match.score > .8 ? "warning" : ""}`} key={match.id}><strong>{Math.round(match.score * 100)}%</strong><div><b>PR #{match.old_pr_number}</b><span>{match.relationship}</span><MatchComponents components={match.components} /></div></div>) : <p className="no-conflict">✓ No strong historical conflicts</p>}</article>; })}</div>;
}

function MatchComponents({ components }: { components?: Components }) {
  if (!components) return null;
  const pct = (value: number) => `${Math.round(value * 100)}%`;
  const items: [string, string, boolean][] = [
    ["semantic", components.semantic == null ? "unavailable" : pct(components.semantic), components.semantic == null],
    ["title/body", pct(components.title_body), false],
    ["files", pct(components.files), false],
    ["modules", pct(components.paths), false],
    ["approach", pct(components.approach), false],
  ];
  return <p className="components">{items.map(([label, value, unavailable]) => <span key={label} className={unavailable ? "unavailable" : ""}>{label} {value}</span>)}</p>;
}

function GraphView({ graph, loading }: { graph?: Graph; loading: boolean }) {
  if (loading) return <p>Loading decision graph...</p>;
  const nodes = graph?.nodes || [];
  const edges = graph?.edges || [];
  if (!nodes.length) return <div className="empty page-empty">The graph will grow as Tombstones and similarity matches are created.</div>;
  const labels = new Map(nodes.map((node) => [node.id, node.label]));
  return <div className="graph-layout"><section className="graph-summary"><strong>{nodes.length}</strong><span>nodes</span><strong>{edges.length}</strong><span>relations</span></section><div className="edge-list">{edges.map((edge) => <div className="edge" key={edge.id}><span>{labels.get(edge.source) || edge.source}</span><b>{edge.relation.replaceAll("_", " ")} →</b><span>{labels.get(edge.target) || edge.target}</span></div>)}</div></div>;
}

function SettingsView({ repo, value, loading, token, queryClient }: { repo: Repo; value?: Settings; loading: boolean; token: string; queryClient: ReturnType<typeof useQueryClient> }) {
  const [form, setForm] = useState<Settings | null>(null); const [scope, setScope] = useState("50");
  useEffect(() => { if (value) setForm(value); }, [value]);
  const save = useMutation({ mutationFn: (settings: Settings) => apiJSON<Settings>(`/api/repositories/${repo.id}/settings`, token, { method: "PUT", body: JSON.stringify(settings) }), onSuccess: (settings) => { setForm(settings); queryClient.invalidateQueries({ queryKey: ["settings", repo.id] }); } });
  const backfill = useMutation({ mutationFn: () => apiJSON<{ queued: number }>(`/api/repositories/${repo.id}/backfill?scope=${scope}`, token, { method: "POST" }), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["jobs"] }) });
  if (loading || !form) return <p>Loading repository settings...</p>;
  return <div className="settings-grid"><section className="panel"><p className="eyebrow">Notifications</p><h3>Historical context delivery</h3><label className="field"><span>Notify mode</span><select value={form.notify_mode} onChange={(event) => setForm({ ...form, notify_mode: event.target.value as Settings["notify_mode"] })}><option value="dashboard">Dashboard only</option><option value="check">GitHub Check</option></select></label><p className="hint">Checks require the optional Checks: write permission. Dashboard mode never writes to GitHub.</p></section>
    <section className="panel"><p className="eyebrow">Data controls</p><h3>Retention and context</h3><label className="field"><span>Retention</span><select value={form.retention_days} onChange={(event) => setForm({ ...form, retention_days: Number(event.target.value) })}><option value={7}>7 days</option><option value={30}>30 days</option><option value={90}>90 days</option><option value={-1}>Forever</option></select></label><label className="toggle"><input type="checkbox" checked={form.contents_enabled} onChange={(event) => setForm({ ...form, contents_enabled: event.target.checked })} /><span>Read CODEOWNERS and CONTRIBUTING context</span></label><button className="primary" onClick={() => save.mutate(form)} disabled={save.isPending}>{save.isPending ? "Saving..." : "Save settings"}</button>{save.isError && <p className="error">{String(save.error)}</p>}</section>
    <section className="panel wide"><p className="eyebrow">History import</p><h3>Backfill closed, unmerged pull requests</h3><div className="backfill"><select value={scope} onChange={(event) => setScope(event.target.value)}><option value="50">Last 50 closed PRs</option><option value="100">Last 100 closed PRs</option><option value="year">Last year</option><option value="all">All available (up to 3,000)</option></select><button className="primary" onClick={() => backfill.mutate()} disabled={backfill.isPending}>{backfill.isPending ? "Queuing..." : "Import history"}</button></div>{backfill.data && <p className="success">Queued {backfill.data.queued} analysis jobs.</p>}{backfill.isError && <p className="error">{String(backfill.error)}</p>}</section></div>;
}

function DetailSection({ label, children }: { label: string; children: React.ReactNode }) { return <section><label>{label}</label>{children}</section>; }
function ClaimList({ label, claims, evidence, fallback }: { label: string; claims?: Claim[] | null; evidence: Evidence[]; fallback?: string }) { const list = claims || []; return <DetailSection label={label}><ul>{list.length ? list.map((claim, index) => <li key={index}>{claim.claim}<EvidenceLinks ids={claim.evidence_ids || []} evidence={evidence} /></li>) : fallback ? <li>{fallback}</li> : <li className="muted">No evidence-backed finding.</li>}</ul></DetailSection>; }
function EvidenceLinks({ ids, evidence }: { ids: string[]; evidence: Evidence[] }) { return <span className="links">{ids.map((id) => { const item = evidence.find((candidate) => candidate.id === id); return item ? <a href={item.source_url || "#"} target="_blank" rel="noreferrer" key={id}>View evidence</a> : null; })}</span>; }
