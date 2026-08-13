"use client";
/* eslint-disable jsx-a11y/no-autofocus -- Focus is moved into the newly opened modal dialog. */
/* eslint-disable @typescript-eslint/no-explicit-any -- Stalwart JMAP payloads are runtime-discovered until the pinned adapter returns them. */
/* eslint-disable react-hooks/set-state-in-effect -- Initial JMAP and admin data are intentionally loaded from effects. */
/* eslint-disable jsx-a11y/no-static-element-interactions -- The modal backdrop supplements its explicit close button. */
/* eslint-disable jsx-a11y/no-noninteractive-element-to-interactive-role -- Mail rows are keyboard-operable composites with nested selection and star controls. */

import DOMPurify from "dompurify";
import {
  AlertTriangle,
  Archive,
  ArrowLeft,
  ArrowRight,
  Bell,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleUserRound,
  Clock3,
  Copy,
  Download,
  FileText,
  Forward,
  Gauge,
  Globe2,
  HelpCircle,
  Inbox,
  KeyRound,
  LifeBuoy,
  Mail,
  Menu,
  Moon,
  MoreHorizontal,
  Paperclip,
  Pencil,
  Plus,
  RefreshCw,
  Reply,
  Search,
  Send,
  Server,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Smile,
  Sparkles,
  Star,
  Sun,
  Tag,
  Trash2,
  Users,
  Webhook,
  X,
  Zap,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

type FolderName = "Inbox" | "Starred" | "Snoozed" | "Sent" | "Drafts" | "Archive" | "Spam" | "Trash";
type Message = {
  id: number | string;
  sender: string;
  email: string;
  initials: string;
  color: string;
  subject: string;
  preview: string;
  time: string;
  date: string;
  unread: boolean;
  starred: boolean;
  labels: string[];
  folder: FolderName;
  body: string;
  attachments?: { name: string; size: string; kind: string }[];
  threadCount?: number;
  serverId?: string;
  mailboxIds?: string[];
};

type ComposeMessage = { to: string; cc: string; bcc: string; subject: string; body: string };

const initialMessages: Message[] = [
  {
    id: 1,
    sender: "Lina Park",
    email: "lina@northstar.studio",
    initials: "LP",
    color: "coral",
    subject: "Q3 design review — final notes",
    preview: "I pulled the final comments into one place. The new navigation feels much calmer now…",
    time: "10:42",
    date: "Aug 12, 2026, 10:42 AM",
    unread: true,
    starred: true,
    labels: ["Design"],
    folder: "Inbox",
    threadCount: 4,
    body: `<p>Hi Alex,</p><p>I pulled the final comments into one place. The new navigation feels much calmer now, and the product team is aligned on the reduced color palette.</p><p>The only open item is the export flow. I’ve attached the marked-up review so we can close it in tomorrow’s session.</p><p>Thanks,<br><strong>Lina</strong></p>`,
    attachments: [{ name: "Q3-design-review.pdf", size: "2.4 MB", kind: "PDF" }],
  },
  {
    id: 2,
    sender: "GitHub",
    email: "notifications@github.com",
    initials: "GH",
    color: "ink",
    subject: "[acme/platform] Production deploy succeeded",
    preview: "Deployment #2841 completed successfully in 3m 12s. All health checks are passing.",
    time: "9:18",
    date: "Aug 12, 2026, 9:18 AM",
    unread: true,
    starred: false,
    labels: ["Updates"],
    folder: "Inbox",
    body: `<p><strong>Production deploy succeeded</strong></p><p>Deployment <code>#2841</code> completed successfully in 3m 12s.</p><p>All 18 health checks are passing. Version <strong>2026.08.12-rc3</strong> is now serving 100% of production traffic.</p>`,
  },
  {
    id: 3,
    sender: "Owen Chen",
    email: "owen@springlabs.io",
    initials: "OC",
    color: "violet",
    subject: "Re: Partnership timeline",
    preview: "Thursday at 2 PM works. I’ll bring our solutions architect so we can cover the SSO questions…",
    time: "8:51",
    date: "Aug 12, 2026, 8:51 AM",
    unread: false,
    starred: false,
    labels: ["Clients"],
    folder: "Inbox",
    threadCount: 3,
    body: `<p>Thursday at 2 PM works for us.</p><p>I’ll bring our solutions architect so we can cover the SSO and data-residency questions in the same call. The pilot scope looks right from our side.</p><p>Looking forward to it,<br>Owen</p>`,
  },
  {
    id: 4,
    sender: "Maya Thompson",
    email: "maya@meovv.company",
    initials: "MT",
    color: "blue",
    subject: "Team offsite: venue options",
    preview: "I found three places that fit the dates and budget. My vote is for the lakeside workshop space…",
    time: "Yesterday",
    date: "Aug 11, 2026, 4:26 PM",
    unread: false,
    starred: true,
    labels: ["Team"],
    folder: "Inbox",
    body: `<p>Hey team,</p><p>I found three places that fit the dates and budget. My vote is for the lakeside workshop space—the main room has enough wall space for planning, and there’s a quiet garden for one-on-ones.</p><p>Please add your preference before Friday.</p>`,
    attachments: [{ name: "venue-comparison.xlsx", size: "148 KB", kind: "XLSX" }],
  },
  {
    id: 5,
    sender: "Postmaster",
    email: "postmaster@mail.meovv.company",
    initials: "PM",
    color: "green",
    subject: "Weekly deliverability report",
    preview: "98.7% of outbound mail was accepted on the first attempt. No blocklist listings detected.",
    time: "Yesterday",
    date: "Aug 11, 2026, 7:00 AM",
    unread: false,
    starred: false,
    labels: ["System"],
    folder: "Inbox",
    body: `<p>Your weekly deliverability summary is ready.</p><ul><li><strong>98.7%</strong> accepted on first attempt</li><li><strong>0.3%</strong> temporarily deferred</li><li><strong>0</strong> blocklist listings detected</li></ul><p>SPF, DKIM, and DMARC are aligned for all active domains.</p>`,
  },
  {
    id: 6,
    sender: "Nora Ibrahim",
    email: "nora@meovv.company",
    initials: "NI",
    color: "amber",
    subject: "Updated hiring plan",
    preview: "Finance approved the two platform roles. I’ve updated the headcount model and interview loop…",
    time: "Mon",
    date: "Aug 10, 2026, 3:14 PM",
    unread: false,
    starred: false,
    labels: ["Team"],
    folder: "Inbox",
    body: `<p>Finance approved the two platform roles.</p><p>I’ve updated the headcount model and interview loop. We can publish both positions after your final review.</p>`,
    attachments: [{ name: "hiring-plan-v4.pdf", size: "864 KB", kind: "PDF" }],
  },
  {
    id: 7,
    sender: "Linear",
    email: "updates@linear.app",
    initials: "LI",
    color: "violet",
    subject: "Your workspace digest",
    preview: "14 issues completed, 9 created, and 2 projects moved to the next milestone this week.",
    time: "Mon",
    date: "Aug 10, 2026, 8:05 AM",
    unread: false,
    starred: false,
    labels: ["Updates"],
    folder: "Inbox",
    body: `<p>Here’s what happened in your workspace this week:</p><p><strong>14</strong> issues completed · <strong>9</strong> created · <strong>2</strong> projects advanced</p>`,
  },
  {
    id: 8,
    sender: "Alex Morgan",
    email: "meghdad@meovv.company",
    initials: "ME",
    color: "blue",
    subject: "Re: Architecture decision record",
    preview: "The signed decision record and follow-up actions are attached for the platform group.",
    time: "Sun",
    date: "Aug 9, 2026, 2:31 PM",
    unread: false,
    starred: false,
    labels: ["Engineering"],
    folder: "Sent",
    body: `<p>Hi all,</p><p>The signed decision record and follow-up actions are attached for the platform group. Please use this as the source of truth going forward.</p>`,
  },
];

const folders: { name: FolderName; icon: typeof Inbox; count?: number }[] = [
  { name: "Inbox", icon: Inbox, count: 5 },
  { name: "Starred", icon: Star },
  { name: "Snoozed", icon: Clock3 },
  { name: "Sent", icon: Send },
  { name: "Drafts", icon: FileText, count: 2 },
  { name: "Archive", icon: Archive },
  { name: "Spam", icon: ShieldCheck },
  { name: "Trash", icon: Trash2 },
];

const adminNav = [
  { id: "overview", label: "Overview", icon: Gauge },
  { id: "domains", label: "Domains", icon: Globe2 },
  { id: "people", label: "People & aliases", icon: Users },
  { id: "delivery", label: "Delivery", icon: Send },
  { id: "api", label: "API keys", icon: KeyRound },
  { id: "webhooks", label: "Webhooks", icon: Webhook },
  { id: "backups", label: "Backups", icon: Download },
];

function SafeHtml({ html }: { html: string }) {
  const sanitized = useMemo(() => DOMPurify.sanitize(html, { FORBID_TAGS: ["style", "iframe", "form"], FORBID_ATTR: ["style"] }), [html]);
  return <div className="message-body" dir="auto" dangerouslySetInnerHTML={{ __html: sanitized }} />;
}

function Logo({ compact = false }: { compact?: boolean }) {
  return (
    <div className="brand" aria-label="MEOVV Mail">
      <span className="brand-mark"><Mail size={18} strokeWidth={2.25} /></span>
      {!compact && <><span className="brand-name">MEOVV</span><span className="brand-product">Mail</span></>}
    </div>
  );
}

function base64url(bytes: ArrayBuffer) {
  return btoa(String.fromCharCode(...new Uint8Array(bytes))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function escapeHTML(value: string) {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function addressList(value: string) {
  return value.split(",").map((email) => email.trim()).filter(Boolean).map((email) => ({ email }));
}

function Login({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [account, setAccount] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const signIn = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true); setError("");
    try {
      const verifier = base64url(crypto.getRandomValues(new Uint8Array(48)).buffer);
      const challenge = base64url(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier)));
      const redirectUri = `${location.origin}/auth/callback`;
      const authorization = await fetch("/api/auth", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type: "authCode", accountName: account, secret: password, clientId: "meovv-web", redirectUri, codeChallenge: challenge, codeChallengeMethod: "S256" }),
      });
      const authorized = await authorization.json();
      const clientCode = authorized.clientCode ?? authorized.client_code ?? authorized.code;
      if (!authorization.ok || authorized.authenticated === false || !clientCode) throw new Error(authorized.detail ?? authorized.error ?? "The email address or password was not accepted.");
      setPassword("");
      const exchange = await fetch("/api/session/exchange", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ client_code: clientCode, code_verifier: verifier, account, client_id: "meovv-web", redirect_uri: redirectUri }),
      });
      const result = await exchange.json();
      if (!exchange.ok) throw new Error(result.detail ?? "Could not establish a secure session.");
      onAuthenticated();
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Sign-in failed."); }
    finally { setBusy(false); }
  };
  return <div className="login-screen"><aside className="login-story"><Logo /><div><span className="eyebrow">YOUR MAIL. YOUR INFRASTRUCTURE.</span><h1>Welcome back.</h1><p>A calm, private inbox for work that matters.</p></div><span className="login-security"><ShieldCheck /> Credentials go directly to your mail server.</span></aside><main><form className="login-card" onSubmit={signIn}><span className="login-icon"><Mail /></span><h2>Sign in to MEOVV Mail</h2><p>Use your organization email account.</p><label>Email address<input type="email" autoComplete="username" required value={account} onChange={(event) => setAccount(event.target.value)} placeholder="you@example.com" /></label><label>Password<input type="password" autoComplete="current-password" required value={password} onChange={(event) => setPassword(event.target.value)} /></label>{error && <div className="form-error" role="alert"><AlertTriangle />{error}</div>}<button className="primary login-submit" disabled={busy}>{busy ? <><RefreshCw className="spin" /> Signing in…</> : <>Sign in <ArrowRight /></>}</button><small>Protected with OAuth authorization code and PKCE.</small></form></main></div>;
}

function Avatar({ message, small = false }: { message: Message; small?: boolean }) {
  return <span className={`avatar ${message.color} ${small ? "small" : ""}`}>{message.initials}</span>;
}

function Compose({ onClose, onSend }: { onClose: () => void; onSend: (message: ComposeMessage) => Promise<void> | void }) {
  const [more, setMore] = useState(false);
  const [to, setTo] = useState("");
  const [cc, setCC] = useState("");
  const [bcc, setBCC] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [status, setStatus] = useState("Draft saved");
  useEffect(() => {
    const timer = setTimeout(() => setStatus("Draft saved"), 650);
    return () => clearTimeout(timer);
  }, [subject, body]);
  return (
    <div className="compose" role="dialog" aria-modal="true" aria-label="New message">
      <header><div><span className="compose-dot" />New message</div><div className="compose-actions"><button aria-label="Minimize"><span>—</span></button><button onClick={onClose} aria-label="Close"><X size={17} /></button></div></header>
      <div className="compose-fields">
        <label><span>To</span><input required aria-label="Recipients" placeholder="name@example.com" value={to} onChange={(e) => setTo(e.target.value)} /></label>
        <button className="cc-button" onClick={() => setMore(!more)}>Cc Bcc</button>
        {more && <><label><span>Cc</span><input aria-label="CC recipients" value={cc} onChange={(e) => setCC(e.target.value)} /></label><label><span>Bcc</span><input aria-label="BCC recipients" value={bcc} onChange={(e) => setBCC(e.target.value)} /></label></>}
        <label><input aria-label="Subject" placeholder="Subject" value={subject} onChange={(e) => { setSubject(e.target.value); setStatus("Saving…"); }} /></label>
      </div>
      <textarea aria-label="Message body" placeholder="Write your message…" value={body} onChange={(e) => { setBody(e.target.value); setStatus("Saving…"); }} />
      <footer>
        <div className="compose-tools"><button className="send-button" disabled={!to.trim()} onClick={() => onSend({ to, cc, bcc, subject, body })}><Send size={16} /> Send <kbd>⌘↵</kbd></button><button aria-label="Formatting"><span className="format-a">A</span></button><button aria-label="Attach file"><Paperclip size={18} /></button><button aria-label="Insert emoji"><Smile size={18} /></button></div>
        <div className="compose-status"><span>{status}</span><button aria-label="Discard"><Trash2 size={17} /></button></div>
      </footer>
    </div>
  );
}

function SetupWizard({ onComplete }: { onComplete: () => void }) {
  const [step, setStep] = useState(1);
  const [delivery, setDelivery] = useState<"direct" | "relay">("direct");
  const [form, setForm] = useState({ hostname: "mail.example.com", domain: "example.com", organization: "My organization", admin: "admin@example.com", accent: "#6d4aff", relay: "", token: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [bootstrapResult, setBootstrapResult] = useState<unknown>(null);
  const set = (key: keyof typeof form, value: string) => setForm((current) => ({ ...current, [key]: value }));
  const finish = async () => {
    setBusy(true); setError("");
    try {
      const response = await fetch("/api/setup/complete", { method: "POST", headers: { "Content-Type": "application/json", "X-Bootstrap-Token": form.token }, body: JSON.stringify({ hostname: form.hostname, primary_domain: form.domain, admin_email: form.admin, organization: form.organization, accent: form.accent, delivery_mode: delivery, relay_host: delivery === "relay" ? form.relay : "" }) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.detail ?? "Setup could not be completed.");
      setBootstrapResult(result.mail_core_result ?? { configured: true });
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Setup failed."); }
    finally { setBusy(false); }
  };
  if (bootstrapResult) return <div className="setup-finished"><div><span className="step-icon"><CheckCircle2 /></span><h1>Your mail appliance is configured.</h1><p>Save the one-time mail-core result below, then run <code>sudo ./scripts/install-server.sh finalize</code> on the server. Finalization restarts the mail core and pauses so you can verify this administrator before recovery access is removed.</p><pre>{JSON.stringify(bootstrapResult, null, 2)}</pre><div className="setup-warning"><ShieldCheck />This result is not stored by MEOVV Mail.</div><button className="primary" onClick={onComplete}>I saved it — show sign in <ArrowRight /></button></div></div>;
  return (
    <div className="setup-screen">
      <aside className="setup-aside">
        <Logo />
        <div className="setup-copy"><span className="eyebrow">PRIVATE EMAIL, BEAUTIFULLY RUN</span><h1>Your organization’s inbox, on your infrastructure.</h1><p>A secure, modern mail service with no maze of components to maintain.</p></div>
        <div className="setup-assurance"><ShieldCheck size={20} /><div><strong>Your data stays yours</strong><span>Messages never leave infrastructure you control.</span></div></div>
      </aside>
      <main className="setup-main">
        <div className="setup-progress"><span>Setup</span><div>{[1,2,3].map((n) => <i key={n} className={n <= step ? "active" : ""} />)}</div><span>{step} of 3</span></div>
        {step === 1 && <section><span className="step-icon"><Globe2 /></span><h2>Let’s name your mail server</h2><p>This is the public address mail apps and other servers will use.</p><label>Mail server hostname<input value={form.hostname} onChange={(e) => set("hostname", e.target.value)} /></label><label>Primary email domain<input value={form.domain} onChange={(e) => set("domain", e.target.value)} /></label><label>One-time bootstrap token<input type="password" value={form.token} onChange={(e) => set("token", e.target.value)} placeholder="Printed by mailctl init" /></label><div className="field-note"><CheckCircle2 /> You can add more domains later.</div></section>}
        {step === 2 && <section><span className="step-icon"><CircleUserRound /></span><h2>Create your organization</h2><p>Choose the name and administrator for this installation.</p><label>Organization name<input value={form.organization} onChange={(e) => set("organization", e.target.value)} /></label><label>Administrator email<input type="email" value={form.admin} onChange={(e) => set("admin", e.target.value)} /></label><label>Accent color<div className="accent-choice">{["#6d4aff", "#9d4276", "#2674d9", "#25856d"].map((color, index) => <button key={color} aria-label={`Choose accent ${index + 1}`} style={{ background: color }} className={`color-choice ${form.accent === color ? "active" : ""}`} onClick={() => set("accent", color)} />)}</div></label><div className="field-note"><ShieldCheck /> The mail core provisions a permanent administrator during bootstrap.</div></section>}
        {step === 3 && <section><span className="step-icon"><Send /></span><h2>How should mail leave?</h2><p>You can change this any time without reinstalling.</p><button className={`delivery-choice ${delivery === "direct" ? "active" : ""}`} onClick={() => setDelivery("direct")}><span><Zap /></span><div><strong>Deliver directly</strong><p>Best when your server has a static IP and reverse DNS.</p></div>{delivery === "direct" && <CheckCircle2 />}</button><button className={`delivery-choice ${delivery === "relay" ? "active" : ""}`} onClick={() => setDelivery("relay")}><span><Server /></span><div><strong>Use a smart host</strong><p>Route outbound messages through an authenticated relay.</p></div>{delivery === "relay" && <CheckCircle2 />}</button>{delivery === "relay" && <label>Relay host<input value={form.relay} onChange={(e) => set("relay", e.target.value)} placeholder="smtp.example.com:587" /></label>}{error && <div className="form-error"><AlertTriangle />{error}</div>}</section>}
        <footer><button className="secondary" disabled={step === 1 || busy} onClick={() => setStep(step - 1)}>Back</button><button className="primary" disabled={busy} onClick={() => step < 3 ? setStep(step + 1) : finish()}>{busy ? "Configuring…" : step < 3 ? "Continue" : "Finish setup"}{busy ? <RefreshCw className="spin" /> : <ArrowRight size={17} />}</button></footer>
      </main>
    </div>
  );
}

function AdminDashboard({ onMail }: { onMail: () => void }) {
  const [section, setSection] = useState("overview");
  return (
    <div className="admin-shell">
      <aside className="admin-sidebar">
        <Logo />
        <nav>{adminNav.map(({ id, label, icon: Icon }) => <button key={id} className={section === id ? "active" : ""} onClick={() => setSection(id)}><Icon size={18} />{label}{id === "delivery" && <span className="nav-badge">2</span>}</button>)}</nav>
        <div className="sidebar-bottom"><button><LifeBuoy />Help & docs</button><button><Settings />Settings</button><button className="admin-user"><span>AM</span><div><strong>Alex Morgan</strong><small>Administrator</small></div><MoreHorizontal /></button></div>
      </aside>
      <main className="admin-main">
        <header className="admin-top"><button className="mobile-menu"><Menu /></button><div><h1>{adminNav.find((item) => item.id === section)?.label}</h1><p>mail.meovv.company</p></div><div className="admin-top-actions"><button className="view-inbox" onClick={onMail}><Inbox size={16} /> Open inbox</button><button aria-label="Notifications"><Bell size={19} /><i /></button></div></header>
        {section === "overview" ? <Overview /> : <AdminSection section={section} />}
      </main>
    </div>
  );
}

function Overview() {
  const bars = [34, 44, 39, 62, 51, 58, 72, 66, 84, 73, 89, 82, 91, 78, 96, 88, 104, 94, 112, 101, 118, 108, 124, 117];
  return (
    <div className="overview">
      <section className="status-banner"><span><CheckCircle2 /></span><div><strong>Everything is running smoothly</strong><p>All services are healthy. Last checked just now.</p></div><button>View diagnostics <ChevronRight /></button></section>
      <div className="metric-grid">
        <article><header><span className="metric-icon purple"><Send /></span><small>Last 24 hours</small></header><strong>12,486</strong><p>Messages handled <em>+8.4%</em></p></article>
        <article><header><span className="metric-icon green"><Check /></span><small>First attempt</small></header><strong>98.7%</strong><p>Delivery rate <em>+0.6%</em></p></article>
        <article><header><span className="metric-icon blue"><Users /></span><small>500 limit</small></header><strong>184</strong><p>Active mailboxes</p></article>
        <article><header><span className="metric-icon amber"><Server /></span><small>2 TB available</small></header><strong>684 GB</strong><p>Storage used <span className="storage-line"><i /></span></p></article>
      </div>
      <div className="overview-grid">
        <article className="delivery-chart">
          <header><div><h2>Mail activity</h2><p>Messages accepted and sent</p></div><button>Last 24 hours <ChevronDown /></button></header>
          <div className="chart"><div className="y-labels"><span>150</span><span>100</span><span>50</span><span>0</span></div><div className="bars">{bars.map((height, i) => <span key={i} style={{ height: `${height}px` }} className={i > 18 ? "recent" : ""} />)}</div></div>
          <div className="x-labels"><span>12 AM</span><span>6 AM</span><span>12 PM</span><span>6 PM</span><span>Now</span></div>
        </article>
        <article className="attention-card"><header><h2>Needs attention</h2><span>2</span></header><div className="attention-item"><span className="warning"><AlertTriangle /></span><div><strong>Reverse DNS not detected</strong><p>Outbound delivery may be affected.</p><button>Review DNS <ArrowRight /></button></div></div><div className="attention-item"><span className="info"><Webhook /></span><div><strong>Webhook retrying</strong><p>billing-production has failed 3 times.</p><button>View attempts <ArrowRight /></button></div></div></article>
      </div>
      <div className="overview-grid lower">
        <article className="domain-card"><header><div><h2>Domains</h2><p>3 domains configured</p></div><button>Manage</button></header>{[["meovv.company","Primary","Healthy"],["studio-meovv.com","","Healthy"],["meovv.dev","","DKIM pending"]].map(([domain,badge,status]) => <div className="domain-row" key={domain}><span className="domain-icon"><Globe2 /></span><div><strong>{domain}</strong><small>{badge || "Email domain"}</small></div><span className={status === "Healthy" ? "healthy" : "pending"}><i />{status}</span></div>)}</article>
        <article className="recent-card"><header><div><h2>Recent delivery</h2><p>Live status from the mail queue</p></div><button><RefreshCw /></button></header>{[["Quarterly report","sara@northstar.com","Delivered","12 sec"],["Welcome to MEOVV","alex@meovv.dev","Delivered","1 min"],["Invoice #1042","billing@client.io","Deferred","4 min"]].map(([subject,to,status,time]) => <div className="delivery-row" key={subject}><span className={status.toLowerCase()}>{status === "Delivered" ? <Check /> : <Clock3 />}</span><div><strong>{subject}</strong><small>to {to}</small></div><em>{time}</em></div>)}</article>
      </div>
    </div>
  );
}

function AdminSection({ section }: { section: string }) {
  const [items, setItems] = useState<any[]>([]);
  const [modal, setModal] = useState(false);
  const [name, setName] = useState("");
  const [senders, setSenders] = useState("");
  const [url, setURL] = useState("");
  const [createdSecret, setCreatedSecret] = useState("");
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const dynamic = section === "api" || section === "webhooks";
  const load = useCallback(async () => {
    if (!dynamic) return;
    await Promise.resolve();
    setItems([]); setLoaded(false);
    try { const response = await fetch(`/api/admin/${section === "api" ? "api-keys" : "webhooks"}`); if (response.ok) { setItems((await response.json()).data ?? []); setLoaded(true); } } catch { /* The design preview uses representative rows. */ }
  }, [dynamic, section]);
  useEffect(() => { void load(); }, [load]);
  const content: Record<string, { title: string; description: string; action: string; icon: typeof Globe2; rows: string[][] }> = {
    domains: { title: "Email domains", description: "Manage sender identity and DNS health.", action: "Add domain", icon: Globe2, rows: [["meovv.company", "Primary domain", "Healthy"], ["studio-meovv.com", "184 addresses", "Healthy"], ["meovv.dev", "24 addresses", "DKIM pending"]] },
    people: { title: "People & aliases", description: "Accounts, addresses, aliases, and quotas.", action: "Add person", icon: Users, rows: [["Alex Morgan", "alex@meovv.example", "Administrator"], ["Maya Thompson", "maya@meovv.example", "24.6 GB of 50 GB"], ["Nora Ibrahim", "nora@meovv.example", "18.2 GB of 50 GB"]] },
    delivery: { title: "Delivery center", description: "Follow queued, deferred, and failed mail.", action: "Run diagnostics", icon: Send, rows: [["Quarterly report", "northstar.com", "Delivered"], ["Invoice #1042", "client.io", "Deferred"], ["Password reset", "example.net", "Delivered"]] },
    api: { title: "API keys", description: "Scoped credentials for application delivery.", action: "Create API key", icon: KeyRound, rows: [["Production app", "meovv_live_8K…", "Used 4 min ago"], ["Billing service", "meovv_live_3F…", "Used yesterday"], ["Staging", "meovv_test_9Q…", "Never used"]] },
    webhooks: { title: "Webhook endpoints", description: "Signed, at-least-once delivery events.", action: "Add endpoint", icon: Webhook, rows: [["billing-production", "5 event types", "Retrying"], ["analytics", "3 event types", "Healthy"], ["audit mirror", "All failures", "Healthy"]] },
    backups: { title: "Backups", description: "Verified snapshots of mail, configuration, and secrets.", action: "Create backup", icon: Download, rows: [["backup-2026-08-12", "684 GB", "Verified"], ["backup-2026-08-11", "681 GB", "Verified"], ["backup-2026-08-10", "677 GB", "Verified"]] },
  };
  const item = content[section] ?? content.domains;
  const liveRows = dynamic && loaded ? items.map((entry) => section === "api" ? [entry.name, `${entry.prefix}… · ${entry.scopes?.join(", ")}`, entry.last_used_at ? "Active" : "Never used", entry.id] : [entry.name, `${entry.events?.length ?? 0} event types · ${entry.url}`, entry.enabled ? "Healthy" : "Paused", entry.id]) : item.rows;
  const create = async (event: React.FormEvent) => {
    event.preventDefault(); setError("");
    try {
      const endpoint = section === "api" ? "api-keys" : "webhooks";
      const body = section === "api" ? { name, scopes: ["messages.send", "messages.status"], allowed_senders: senders.split(",").map((value) => value.trim()).filter(Boolean), rate_limit: 60 } : { name, url, events: ["message.queued", "message.delivered", "message.deferred", "message.bounced", "message.failed"] };
      const response = await fetch(`/api/admin/${endpoint}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.detail ?? "Could not create this item.");
      setCreatedSecret(result.secret); setName(""); setSenders(""); setURL(""); void load();
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Request failed."); }
  };
  const remove = async (id: string) => {
    const endpoint = section === "api" ? "api-keys" : "webhooks";
    const response = await fetch(`/api/admin/${endpoint}/${id}`, { method: "DELETE" });
    if (response.ok) { setItems((current) => current.filter((entry) => entry.id !== id)); }
  };
  return <div className="admin-section"><div className="section-heading"><div><h2>{item.title}</h2><p>{item.description}</p></div><button onClick={() => dynamic && setModal(true)}><Plus />{item.action}</button></div><div className="settings-card"><div className="table-head"><span>Name</span><span>Details</span><span>Status</span><span /></div>{liveRows.length === 0 ? <div className="admin-empty"><item.icon /><strong>Nothing here yet</strong><span>{item.description}</span></div> : liveRows.map((row) => <div className="settings-row" key={row[3] ?? row[0]}><span className="row-icon"><item.icon /></span><strong>{row[0]}</strong><span>{row[1]}</span><em className={row[2].match(/Healthy|Verified|Delivered|Administrator|Active/) ? "good" : "warn"}><i />{row[2]}</em>{dynamic && row[3] ? <button onClick={() => void remove(row[3])} aria-label={`Remove ${row[0]}`}><Trash2 /></button> : <button><MoreHorizontal /></button>}</div>)}</div>{modal && <div className="admin-modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setModal(false)}><form className="admin-modal" onSubmit={create}><header><span className="row-icon">{section === "api" ? <KeyRound /> : <Webhook />}</span><div><h3>{section === "api" ? "Create API key" : "Add webhook endpoint"}</h3><p>{section === "api" ? "The secret will be shown only once." : "Events are signed and retried for 24 hours."}</p></div><button type="button" onClick={() => setModal(false)}><X /></button></header>{createdSecret ? <div className="secret-result"><CheckCircle2 /><strong>Created successfully</strong><p>Copy this secret now. It cannot be recovered later.</p><code>{createdSecret}</code><button type="button" onClick={() => navigator.clipboard.writeText(createdSecret)}><Copy /> Copy secret</button></div> : <><label>Name<input autoFocus required value={name} onChange={(event) => setName(event.target.value)} placeholder={section === "api" ? "Production application" : "Billing production"} /></label>{section === "api" ? <label>Approved senders<input required value={senders} onChange={(event) => setSenders(event.target.value)} placeholder="notifications@example.com, *@example.com" /><small>Send and status scopes · 60 submissions per minute</small></label> : <label>HTTPS endpoint URL<input required type="url" value={url} onChange={(event) => setURL(event.target.value)} placeholder="https://example.com/hooks/mail" /><small>All five delivery event types are selected.</small></label>}{error && <div className="form-error"><AlertTriangle />{error}</div>}<footer><button type="button" onClick={() => setModal(false)}>Cancel</button><button className="primary">Create</button></footer></>}</form></div>}</div>;
}

function MailApp({ onAdmin, onSetup }: { onAdmin: () => void; onSetup: () => void }) {
  const [messages, setMessages] = useState(initialMessages);
  const [folder, setFolder] = useState<FolderName>("Inbox");
  const [selected, setSelected] = useState<number | string>(1);
  const [checked, setChecked] = useState<(number | string)[]>([]);
  const [search, setSearch] = useState("");
  const [compose, setCompose] = useState(false);
  const [toast, setToast] = useState("");
  const [theme, setTheme] = useState<"light" | "dark">("light");
  const [sidebar, setSidebar] = useState(false);
  const [jmap, setJmap] = useState<{ accountId: string; identityId: string; email: string; mailboxes: Record<string, string>; connected: boolean }>({ accountId: "", identityId: "", email: "", mailboxes: {}, connected: false });
  const searchRef = useRef<HTMLInputElement>(null);
  const filtered = useMemo(() => messages.filter((m) => {
    const belongs = folder === "Starred" ? m.starred : m.folder === folder;
    return belongs && `${m.sender} ${m.subject} ${m.preview}`.toLowerCase().includes(search.toLowerCase());
  }), [messages, folder, search]);
  const active = messages.find((m) => m.id === selected) ?? filtered[0];

  const syncMail = useCallback(async () => {
    try {
      const sessionResponse = await fetch("/api/mail/session", { headers: { Accept: "application/json" } });
      if (!sessionResponse.ok || !(sessionResponse.headers.get("content-type") ?? "").includes("json")) return;
      const session = await sessionResponse.json();
      const accountId = session.primaryAccounts?.["urn:ietf:params:jmap:mail"] ?? Object.keys(session.accounts ?? {})[0];
      if (!accountId) return;
      const response = await fetch("/api/mail/jmap", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ using: ["urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail", "urn:ietf:params:jmap:submission"], methodCalls: [
        ["Mailbox/get", { accountId, properties: ["id", "name", "role", "totalEmails", "unreadEmails"] }, "mailboxes"],
        ["Identity/get", { accountId }, "identities"],
        ["Email/query", { accountId, sort: [{ property: "receivedAt", isAscending: false }], limit: 100 }, "query"],
        ["Email/get", { accountId, "#ids": { resultOf: "query", name: "Email/query", path: "/ids" }, properties: ["id", "threadId", "mailboxIds", "keywords", "from", "subject", "receivedAt", "preview", "hasAttachment", "bodyValues", "htmlBody", "textBody"] }, "emails"],
      ] }) });
      if (!response.ok) return;
      const result = await response.json();
      const method = (id: string) => result.methodResponses?.find((item: [string, unknown, string]) => item[2] === id)?.[1];
      const mailboxList = method("mailboxes")?.list ?? [];
      const roleByID: Record<string, string> = {};
      const mailboxByRole: Record<string, string> = {};
      for (const mailbox of mailboxList) { roleByID[mailbox.id] = mailbox.role ?? ""; if (mailbox.role) mailboxByRole[mailbox.role] = mailbox.id; }
      const identities = method("identities")?.list ?? [];
      const palette = ["coral", "ink", "violet", "blue", "green", "amber"];
      const mapped: Message[] = (method("emails")?.list ?? []).map((email: any, index: number) => {
        const roles = Object.keys(email.mailboxIds ?? {}).map((id) => roleByID[id]);
        const role = roles.find(Boolean) ?? "inbox";
        const folderMap: Record<string, FolderName> = { inbox: "Inbox", archive: "Archive", drafts: "Drafts", sent: "Sent", junk: "Spam", trash: "Trash" };
        const sender = email.from?.[0] ?? { name: "Unknown sender", email: "" };
        const display = sender.name || sender.email || "Unknown sender";
        const initials = display.split(/\s+/).slice(0, 2).map((part: string) => part[0]).join("").toUpperCase();
        const values = email.bodyValues ?? {};
        const html = (email.htmlBody ?? []).map((part: any) => values[part.partId]?.value ?? "").join("");
        const text = (email.textBody ?? []).map((part: any) => values[part.partId]?.value ?? "").join("\n");
        const received = email.receivedAt ? new Date(email.receivedAt) : new Date();
        return { id: email.id, serverId: email.id, mailboxIds: Object.keys(email.mailboxIds ?? {}), sender: display, email: sender.email ?? "", initials: initials || "?", color: palette[index % palette.length], subject: email.subject || "(No subject)", preview: email.preview ?? text.slice(0, 160), time: received.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }), date: received.toLocaleString([], { dateStyle: "medium", timeStyle: "short" }), unread: !email.keywords?.["$seen"], starred: Boolean(email.keywords?.["$flagged"]), labels: [], folder: folderMap[role] ?? "Inbox", body: html || `<p>${escapeHTML(text).replace(/\n/g, "<br>")}</p>`, attachments: email.hasAttachment ? [{ name: "Attachment", size: "Open message to download", kind: "FILE" }] : undefined };
      });
      setMessages(mapped);
      setSelected((current) => mapped.some((message) => message.id === current) ? current : (mapped[0]?.id ?? ""));
      setJmap({ accountId, identityId: identities[0]?.id ?? "", email: identities[0]?.email ?? session.username ?? "", mailboxes: mailboxByRole, connected: true });
    } catch { /* Local visual preview and transient reconnects retain the current view. */ }
  }, []);

  const notify = useCallback((text: string) => { setToast(text); window.setTimeout(() => setToast(""), 2200); }, []);
  const callJMAP = useCallback(async (methodCalls: unknown[]) => {
    if (!jmap.connected) return null;
    const response = await fetch("/api/mail/jmap", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ using: ["urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail", "urn:ietf:params:jmap:submission"], methodCalls }) });
    if (!response.ok) throw new Error("The mail server did not accept this change.");
    return response.json();
  }, [jmap.connected]);
  const move = (target: FolderName) => {
    const ids = checked.length ? checked : [selected];
    const role: Partial<Record<FolderName, string>> = { Inbox: "inbox", Archive: "archive", Drafts: "drafts", Sent: "sent", Spam: "junk", Trash: "trash" };
    const targetID = role[target] ? jmap.mailboxes[role[target]!] : "";
    for (const message of messages.filter((item) => ids.includes(item.id) && item.serverId)) {
      if (targetID) {
        const mailboxIds: Record<string, boolean | null> = Object.fromEntries((message.mailboxIds ?? []).map((id) => [id, null])); mailboxIds[targetID] = true;
        void callJMAP([["Email/set", { accountId: jmap.accountId, update: { [message.serverId!]: { mailboxIds } } }, "move"]]).catch(() => notify("Move could not be synchronized"));
      }
    }
    setMessages((items) => items.map((m) => ids.includes(m.id) ? { ...m, folder: target, unread: false } : m));
    setChecked([]); notify(`Moved ${ids.length > 1 ? `${ids.length} messages` : "message"} to ${target}`);
  };
  const toggleStar = (id: number | string) => {
    const message = messages.find((item) => item.id === id);
    if (message?.serverId) void callJMAP([["Email/set", { accountId: jmap.accountId, update: { [message.serverId]: { "keywords/$flagged": message.starred ? null : true } } }, "star"]]).catch(() => notify("Star could not be synchronized"));
    setMessages((items) => items.map((m) => m.id === id ? { ...m, starred: !m.starred } : m));
  };
  const openMessage = (id: number | string) => {
    const message = messages.find((item) => item.id === id);
    if (message?.serverId && message.unread) void callJMAP([["Email/set", { accountId: jmap.accountId, update: { [message.serverId]: { "keywords/$seen": true } } }, "read"]]);
    setSelected(id); setMessages((items) => items.map((m) => m.id === id ? { ...m, unread: false } : m));
  };
  const sendComposed = async (draft: ComposeMessage) => {
    if (!jmap.connected) { setCompose(false); notify("Message queued for delivery"); return; }
    try {
      const draftMailbox = jmap.mailboxes.drafts;
      const email = { mailboxIds: draftMailbox ? { [draftMailbox]: true } : {}, keywords: { "$draft": true, "$seen": true }, from: [{ email: jmap.email }], to: addressList(draft.to), cc: addressList(draft.cc), bcc: addressList(draft.bcc), subject: draft.subject, bodyValues: { body: { value: draft.body, charset: "utf-8", isTruncated: false } }, textBody: [{ partId: "body", type: "text/plain" }] };
      const result = await callJMAP([["Email/set", { accountId: jmap.accountId, create: { draft: email } }, "create"], ["EmailSubmission/set", { accountId: jmap.accountId, create: { submission: { emailId: "#draft", identityId: jmap.identityId } } }, "submit"]]);
      if (result?.methodResponses?.some((item: [string]) => item[0] === "error") || result?.methodResponses?.some((item: [string, any]) => item[1]?.notCreated)) throw new Error("The message was not accepted.");
      setCompose(false); notify("Message queued for delivery"); void syncMail();
    } catch (reason) { notify(reason instanceof Error ? reason.message : "Send failed"); }
  };
  useEffect(() => { document.documentElement.dataset.theme = theme; }, [theme]);
  useEffect(() => { void syncMail(); }, [syncMail]);
  useEffect(() => {
    if (!jmap.connected) return;
    const events = new EventSource("/api/mail/events");
    const refresh = () => void syncMail();
    events.onmessage = refresh;
    events.addEventListener("state", refresh);
    return () => events.close();
  }, [jmap.connected, syncMail]);
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "k") { event.preventDefault(); searchRef.current?.focus(); }
      if (event.key === "c" && !["INPUT", "TEXTAREA"].includes((event.target as HTMLElement).tagName)) setCompose(true);
      if (event.key === "Escape") setCompose(false);
    };
    window.addEventListener("keydown", shortcut); return () => window.removeEventListener("keydown", shortcut);
  }, []);

  return (
    <div className="mail-shell">
      <header className="mail-header">
        <button className="mobile-menu" onClick={() => setSidebar(!sidebar)} aria-label="Toggle navigation"><Menu /></button>
        <Logo />
        <label className="search-box"><Search /><input ref={searchRef} value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search mail" aria-label="Search mail" /><kbd>⌘ K</kbd><SlidersHorizontal /></label>
        <div className="header-actions"><span className="sync-status"><i /> All systems operational</span><button onClick={() => setTheme(theme === "light" ? "dark" : "light")} aria-label="Toggle theme">{theme === "light" ? <Moon /> : <Sun />}</button><button aria-label="Help"><HelpCircle /></button><button aria-label="Settings" onClick={onAdmin}><Settings /></button><button className="user-avatar" aria-label="Account menu">AM<span /></button></div>
      </header>
      <div className="mail-layout">
        <aside className={`mail-sidebar ${sidebar ? "open" : ""}`}>
          <button className="compose-button" onClick={() => setCompose(true)}><Pencil /> Compose</button>
          <nav className="folders">{folders.map(({ name, icon: Icon, count }) => <button key={name} className={folder === name ? "active" : ""} onClick={() => { setFolder(name); setSidebar(false); }}><Icon /> <span>{name}</span>{count ? <b>{count}</b> : null}</button>)}</nav>
          <div className="label-heading"><span>Labels</span><button aria-label="Add label"><Plus /></button></div>
          <nav className="labels"><button><i className="label-dot clients" />Clients<span>12</span></button><button><i className="label-dot design" />Design<span>6</span></button><button><i className="label-dot team" />Team<span>9</span></button><button><i className="label-dot updates" />Updates</button></nav>
          <div className="storage-widget"><div><span>Storage</span><strong>34%</strong></div><div className="storage-track"><i /></div><p>17.1 GB of 50 GB used</p><button onClick={onSetup}><Sparkles /> Setup preview</button></div>
        </aside>
        <main className="inbox-panel">
          <div className="list-toolbar">
            <div><button className={checked.length ? "checked" : ""} aria-label="Select all" onClick={() => setChecked(checked.length ? [] : filtered.map((m) => m.id))}>{checked.length ? <Check /> : null}</button><ChevronDown /></div>
            <button aria-label="Refresh" onClick={() => notify("Inbox is up to date")}><RefreshCw /></button>
            {checked.length > 0 && <><span className="toolbar-divider" /><button onClick={() => move("Archive")} aria-label="Archive"><Archive /></button><button onClick={() => move("Spam")} aria-label="Mark as spam"><ShieldCheck /></button><button onClick={() => move("Trash")} aria-label="Delete"><Trash2 /></button></>}
            <button aria-label="More"><MoreHorizontal /></button><span className="range">1–{filtered.length} of {filtered.length}</span><button aria-label="Previous"><ArrowLeft /></button><button aria-label="Next"><ArrowRight /></button>
          </div>
          <div className="message-list" role="list">
            <div className="list-title"><h1>{folder}</h1><span>{filtered.filter((m) => m.unread).length} unread</span></div>
            {filtered.length ? filtered.map((message) => <article key={message.id} role="button" tabIndex={0} className={`${message.id === selected ? "selected" : ""} ${message.unread ? "unread" : ""}`} onClick={() => openMessage(message.id)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); openMessage(message.id); } }}>
              <button className={`row-check ${checked.includes(message.id) ? "active" : ""}`} onClick={(e) => { e.stopPropagation(); setChecked((ids) => ids.includes(message.id) ? ids.filter((id) => id !== message.id) : [...ids, message.id]); }} aria-label={`Select ${message.subject}`}>{checked.includes(message.id) && <Check />}</button>
              <Avatar message={message} />
              <div className="message-summary"><div className="message-meta"><strong>{message.sender}</strong><span>{message.time}</span></div><div className="subject-line"><h2>{message.subject}</h2>{message.threadCount && <b>{message.threadCount}</b>}</div><p>{message.preview}</p><div className="row-labels">{message.labels.map((label) => <span key={label}>{label}</span>)}{message.attachments && <span><Paperclip />{message.attachments.length}</span>}</div></div>
              <button className={`star ${message.starred ? "active" : ""}`} onClick={(e) => { e.stopPropagation(); toggleStar(message.id); }} aria-label="Star message"><Star /></button>
            </article>) : <div className="empty-state"><Inbox /><h2>Nothing here</h2><p>This folder is beautifully empty.</p></div>}
          </div>
        </main>
        {active && <aside className="reader-panel">
          <div className="reader-toolbar"><button className="reader-back" aria-label="Back"><ArrowLeft /></button><button onClick={() => move("Archive")}><Archive /></button><button onClick={() => move("Spam")}><ShieldCheck /></button><button onClick={() => move("Trash")}><Trash2 /></button><span /><button><Clock3 /></button><button><Tag /></button><button><MoreHorizontal /></button></div>
          <div className="reader-content">
            <div className="subject-header"><div><h1>{active.subject}</h1><div>{active.labels.map((label) => <span key={label}>{label}</span>)}</div></div><button className={`star ${active.starred ? "active" : ""}`} onClick={() => toggleStar(active.id)}><Star /></button></div>
            <div className="sender-header"><Avatar message={active} /><div><strong>{active.sender}</strong><p>to me <ChevronDown /></p></div><time>{active.date}</time><button><MoreHorizontal /></button></div>
            <div className="remote-images"><ShieldCheck /><span>Remote images are blocked for your privacy.</span><button onClick={(e) => (e.currentTarget.parentElement!.style.display = "none")}>Show images</button></div>
            <SafeHtml html={active.body} />
            {active.attachments && <div className="attachments"><p><Paperclip /> {active.attachments.length} attachment{active.attachments.length > 1 ? "s" : ""}</p>{active.attachments.map((file) => <button key={file.name}><span><FileText /></span><div><strong>{file.name}</strong><small>{file.kind} · {file.size}</small></div><Download /></button>)}</div>}
            <div className="reply-actions"><button onClick={() => setCompose(true)}><Reply /> Reply</button><button onClick={() => setCompose(true)}><Forward /> Forward</button></div>
          </div>
        </aside>}
      </div>
      {compose && <Compose onClose={() => setCompose(false)} onSend={sendComposed} />}
      {toast && <div className="toast"><CheckCircle2 />{toast}<button onClick={() => setToast("")}><X /></button></div>}
    </div>
  );
}

export default function Home() {
  const [view, setView] = useState<"loading" | "login" | "mail" | "admin" | "setup">("loading");
  useEffect(() => {
    (async () => {
      try {
        const setup = await fetch("/api/setup/status", { headers: { Accept: "application/json" } });
        if (!setup.ok || !(setup.headers.get("content-type") ?? "").includes("json")) throw new Error("preview");
        const status = await setup.json();
        if (!status.configured) { setView("setup"); return; }
        const session = await fetch("/api/session", { headers: { Accept: "application/json" } });
        setView(session.ok ? "mail" : "login");
      } catch { setView("mail"); }
    })();
  }, []);
  if (view === "loading") return <div className="app-loading"><Logo /><RefreshCw className="spin" /><span>Opening your inbox…</span></div>;
  if (view === "login") return <Login onAuthenticated={() => setView("mail")} />;
  return view === "admin" ? <AdminDashboard onMail={() => setView("mail")} /> : view === "setup" ? <SetupWizard onComplete={() => setView("login")} /> : <MailApp onAdmin={() => setView("admin")} onSetup={() => setView("setup")} />;
}
