import type {ReactNode} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

/* ─── Hero ─────────────────────────────────────────────────────────────── */
function Hero() {
  return (
    <header className={styles.hero}>
      {/* Decorative grid */}
      <div className={styles.heroGrid} aria-hidden />
      {/* Glow blobs */}
      <div className={clsx(styles.blob, styles.blobGreen)} aria-hidden />
      <div className={clsx(styles.blob, styles.blobBlue)}  aria-hidden />

      <div className={clsx('container', styles.heroInner)}>
        <div className={styles.heroContent}>
          <span className={styles.badge}>Minimal OCI Orchestration</span>

          <Heading as="h1" className={styles.heroTitle}>
            Container runtime<br />
            <span className={styles.heroAccent}>without the weight.</span>
          </Heading>

          <p className={styles.heroSubtitle}>
            nyxd is a Linux-native OCI orchestrator built on{' '}
            <code>crun</code>. Zero Docker. Zero containerd. Just a clean
            control plane you actually understand.
          </p>

          <div className={styles.heroActions}>
            <Link className={clsx('button button--lg', styles.btnPrimary)} to="/docs/">
              Get started
            </Link>
            <Link className={clsx('button button--lg', styles.btnGhost)} to="/docs/reference/api/">
              API reference
            </Link>
            <Link
              className={clsx('button button--lg', styles.btnOutline)}
              href="https://github.com/zrougamed/nyxd">
              GitHub
            </Link>
          </div>
        </div>

        {/* Terminal preview card */}
        <aside className={styles.terminal} aria-label="Quick start example">
          <div className={styles.terminalBar}>
            <span className={clsx(styles.dot, styles.dotRed)}   />
            <span className={clsx(styles.dot, styles.dotYellow)}/>
            <span className={clsx(styles.dot, styles.dotGreen)} />
            <span className={styles.terminalTitle}>nyxd</span>
          </div>
          <pre className={styles.terminalCode}>{
`# install
make build && sudo install nyxd /usr/local/bin

# start daemon
sudo nyxd --base-dir=/var/lib/nyxd

# run a container
nyx run -d nginx:alpine

# list running containers
nyx ps`
          }</pre>
        </aside>
      </div>
    </header>
  );
}

/* ─── Stats bar ─────────────────────────────────────────────────────────── */
function StatsBar(): ReactNode {
  const stats = [
    {value: 'crun',    label: 'OCI Runtime'},
    {value: 'native',  label: 'Default network'},
    {value: 'no-deps', label: 'Docker / Podman'},
    {value: 'arm64',   label: 'Also supported'},
  ];
  return (
    <div className={styles.statsBar}>
      <div className="container">
        <div className={styles.statsGrid}>
          {stats.map(s => (
            <div key={s.value} className={styles.stat}>
              <span className={styles.statValue}>{s.value}</span>
              <span className={styles.statLabel}>{s.label}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

/* ─── Feature cards ─────────────────────────────────────────────────────── */
type Feature = {tag: string; title: string; body: string; href: string};

const features: Feature[] = [
  {
    tag: 'RUNTIME',
    title: 'No container runtime stack',
    body: 'nyxd calls crun directly. No Docker daemon, no containerd shim, no unnecessary overhead on your host.',
    href: '/docs/',
  },
  {
    tag: 'NETWORK',
    title: 'In-process networking',
    body: 'Native bridge + IPAM + nftables NAT runs fully inside nyxd — no CNI binaries required unless you want them.',
    href: '/docs/operations/networking',
  },
  {
    tag: 'COMPOSE',
    title: 'Compose-compatible workflow',
    body: 'nyx compose up parses a Compose subset — services, env, mounts, depends_on, health checks — no Docker needed.',
    href: '/docs/getting-started/usage',
  },
  {
    tag: 'SUPERVISOR',
    title: 'Supervisor + restart policies',
    body: 'nyxd supervises containers with configurable restart policies and reconciles state after an unclean shutdown.',
    href: '/docs/getting-started/usage',
  },
  {
    tag: 'API',
    title: 'REST control plane',
    body: 'All operations are available via a Unix-socket HTTP API fully documented as an OpenAPI 3 spec.',
    href: '/docs/reference/api/',
  },
  {
    tag: 'SECURITY',
    title: 'Secure by default',
    body: 'NoNewPrivileges, minimal capabilities, masked /proc, per-container network namespace, digest verification on every pull.',
    href: '/docs/',
  },
];

function Features(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className={styles.sectionHeader}>
          <Heading as="h2" className={styles.sectionTitle}>
            Everything you need. Nothing you don&apos;t.
          </Heading>
          <p className={styles.sectionSubtitle}>
            A focused feature set for operators who want predictable container
            orchestration on bare Linux hosts.
          </p>
        </div>
        <div className={styles.featureGrid}>
          {features.map(f => (
            <Link key={f.title} to={f.href} className={styles.featureCard}>
              <span className={styles.featureTag}>{f.tag}</span>
              <strong className={styles.featureTitle}>{f.title}</strong>
              <p className={styles.featureBody}>{f.body}</p>
              <span className={styles.featureMeta}>Read section</span>
            </Link>
          ))}
        </div>
      </div>
    </section>
  );
}

/* ─── CTA ────────────────────────────────────────────────────────────────── */
function Cta(): ReactNode {
  return (
    <section className={styles.cta}>
      <div className="container">
        <div className={styles.ctaInner}>
          <div>
            <Heading as="h2" className={styles.ctaTitle}>
              Ready to ditch the daemon stack?
            </Heading>
            <p className={styles.ctaBody}>
              Read the install guide and have nyxd running in under five minutes
              on any capable Linux host.
            </p>
          </div>
          <div className={styles.ctaActions}>
            <Link className={clsx('button button--lg', styles.btnPrimary)} to="/docs/getting-started/install">
              Install nyxd
            </Link>
            <Link className={clsx('button button--lg', styles.btnGhostDark)} href="https://github.com/zrougamed/nyxd">
              View source
            </Link>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ─── Page ───────────────────────────────────────────────────────────────── */
export default function Home(): ReactNode {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`${siteConfig.title} — ${siteConfig.tagline}`}
      description="Official documentation for nyxd, the minimal OCI container orchestrator for Linux.">
      <Hero />
      <StatsBar />
      <Features />
      <Cta />
    </Layout>
  );
}
