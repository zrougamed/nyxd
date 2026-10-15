import {useEffect, useState, type ReactNode} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import CodeBlock from '@theme/CodeBlock';

import styles from './get-started.module.css';

/* ─── Platforms ─────────────────────────────────────────────────────────── */

type Step = {title: string; time: string; body?: ReactNode; code: string; lang: string};
type Platform = {
  id: string;
  label: string;
  family: 'linux' | 'macos' | 'windows';
  hint: string;
  before?: ReactNode;
  steps: Step[];
  manual?: {title: string; code: string; lang: string};
};

const INSTALL_SH = 'curl -fsSL https://pkgs.nyxd.io/install.sh | sh';

const firstContainer = (curl: string): Step => ({
  title: 'Run your first container',
  time: '~20 s',
  body: <>Start nginx, publish it on port 8080, check it answers, then clean up.</>,
  code: `nyx run -d --name web -p 8080:80 nginx:alpine
${curl}
nyx ps
nyx rm web`,
  lang: 'bash',
});

const linuxSteps = (group: string): Step[] => [
  {
    title: 'Install',
    time: '~30 s',
    body: (
      <>
        Adds the signed nyxd repository for your package manager, installs <code>nyxd</code>,{' '}
        <code>nyx</code> and <code>crun</code>, and starts the service. It asks for <code>sudo</code>.
      </>
    ),
    code: INSTALL_SH,
    lang: 'bash',
  },
  {
    title: 'Use nyx without sudo',
    time: '~5 s',
    body: <>Join the <code>nyx</code> group and pick it up in this shell.</>,
    code: group,
    lang: 'bash',
  },
  firstContainer('curl -s localhost:8080 | grep title'),
];

const usermod = `sudo usermod -aG nyx "$USER"
newgrp nyx
nyx ping`;

const platforms: Platform[] = [
  {
    id: 'ubuntu',
    label: 'Ubuntu · Debian',
    family: 'linux',
    hint: 'apt',
    steps: linuxSteps(usermod),
    manual: {
      title: 'Set up the apt repository by hand',
      lang: 'bash',
      code: `sudo curl -fsSLo /usr/share/keyrings/nyxd.gpg https://pkgs.nyxd.io/nyxd.gpg
echo "deb [signed-by=/usr/share/keyrings/nyxd.gpg] https://pkgs.nyxd.io/apt stable main" | sudo tee /etc/apt/sources.list.d/nyxd.list
sudo apt-get update && sudo apt-get install -y nyxd`,
    },
  },
  {
    id: 'fedora',
    label: 'Fedora · RHEL · Rocky',
    family: 'linux',
    hint: 'dnf / yum',
    steps: linuxSteps(usermod),
    manual: {
      title: 'Set up the dnf repository by hand',
      lang: 'bash',
      code: `sudo curl -fsSLo /etc/yum.repos.d/nyxd.repo https://pkgs.nyxd.io/rpm/nyxd.repo
sudo dnf install -y nyxd`,
    },
  },
  {
    id: 'opensuse',
    label: 'openSUSE · SLES',
    family: 'linux',
    hint: 'zypper',
    steps: linuxSteps(usermod),
    manual: {
      title: 'Set up the zypper repository by hand',
      lang: 'bash',
      code: `sudo zypper addrepo --refresh https://pkgs.nyxd.io/rpm/nyxd.repo
sudo zypper --gpg-auto-import-keys install -y nyxd`,
    },
  },
  {
    id: 'arch',
    label: 'Arch Linux',
    family: 'linux',
    hint: 'pacman',
    steps: linuxSteps(usermod),
    manual: {
      title: 'Set up the pacman repository by hand',
      lang: 'bash',
      code: `curl -fsSL https://pkgs.nyxd.io/nyxd.asc | sudo pacman-key --add -
sudo pacman-key --lsign-key C910E8A22680CCCBBB7E6ED2F0DCFE8A242B1E99
printf '[nyxd]\\nServer = https://pkgs.nyxd.io/arch/$arch\\n' | sudo tee -a /etc/pacman.conf
sudo pacman -Sy nyxd`,
    },
  },
  {
    id: 'alpine',
    label: 'Alpine',
    family: 'linux',
    hint: 'apk',
    steps: linuxSteps(`sudo addgroup "$USER" nyx
su -l "$USER"        # a new login shell with the group
nyx ping`),
    manual: {
      title: 'Set up the apk repository by hand',
      lang: 'bash',
      code: `sudo wget -qO /etc/apk/keys/nyxd.rsa.pub https://pkgs.nyxd.io/alpine/nyxd.rsa.pub
echo https://pkgs.nyxd.io/alpine | sudo tee -a /etc/apk/repositories
sudo apk add nyxd`,
    },
  },
  {
    id: 'linux',
    label: 'Other Linux',
    family: 'linux',
    hint: 'any distribution',
    steps: linuxSteps(usermod),
    manual: {
      title: 'No root? Install for your user only (rootless)',
      lang: 'bash',
      code: `curl -fsSL https://pkgs.nyxd.io/install.sh | sh -s -- --rootless
nyx ping`,
    },
  },
  {
    id: 'macos',
    label: 'macOS',
    family: 'macos',
    hint: 'Apple silicon · Intel',
    before: (
      <>
        Needs <a href="https://brew.sh">Homebrew</a>: the installer uses it for <code>vfkit</code>, which runs
        nyxd in a small Linux VM on Apple&apos;s Virtualization framework.
      </>
    ),
    steps: [
      {
        title: 'Install',
        time: '~40 s',
        body: (
          <>
            Installs <code>nyx</code>, creates the nyx VM, starts it, and starts it again at every login. No{' '}
            <code>sudo</code> needed to use it.
          </>
        ),
        code: INSTALL_SH,
        lang: 'bash',
      },
      {
        title: 'Check the VM answers',
        time: '~5 s',
        code: `nyx machine status
nyx ping`,
        lang: 'bash',
      },
      firstContainer('curl -s localhost:8080 | grep title'),
    ],
  },
  {
    id: 'windows',
    label: 'Windows',
    family: 'windows',
    hint: 'x64 · ARM64',
    before: (
      <>
        Needs WSL 2, enabled once as administrator: <code>wsl --install --no-distribution</code>, then restart.
        After that nothing needs admin rights, and no other distribution is needed.
      </>
    ),
    steps: [
      {
        title: 'Install (PowerShell)',
        time: '~40 s',
        body: (
          <>
            Installs <code>nyx.exe</code> on your <code>PATH</code>, imports the nyx WSL distro, starts nyxd, and
            starts it again at every sign-in.
          </>
        ),
        code: 'irm https://pkgs.nyxd.io/install.ps1 | iex',
        lang: 'powershell',
      },
      {
        title: 'Check it answers',
        time: '~5 s',
        body: <>Open a new terminal so the updated <code>PATH</code> applies.</>,
        code: `nyx machine status
nyx ping`,
        lang: 'powershell',
      },
      {...firstContainer('curl.exe -s localhost:8080 | findstr title'), lang: 'powershell'},
    ],
  },
];

/* ─── Platform detection and URL hash ───────────────────────────────────── */

function detect(): string {
  const hash = window.location.hash.replace('#', '');
  if (platforms.some(p => p.id === hash)) return hash;
  const ua = `${navigator.userAgent} ${navigator.platform ?? ''}`.toLowerCase();
  if (ua.includes('win')) return 'windows';
  if (ua.includes('mac') && !ua.includes('iphone') && !ua.includes('ipad')) return 'macos';
  if (ua.includes('fedora')) return 'fedora';
  if (ua.includes('ubuntu') || ua.includes('debian')) return 'ubuntu';
  if (ua.includes('linux')) return 'ubuntu';
  return 'ubuntu';
}

/* ─── Components ────────────────────────────────────────────────────────── */

function Picker({value, onChange}: {value: string; onChange: (id: string) => void}) {
  const groups: {name: string; family: Platform['family']}[] = [
    {name: 'Linux', family: 'linux'},
    {name: 'Desktop', family: 'macos'},
  ];
  const render = (p: Platform) => (
    <button
      key={p.id}
      type="button"
      role="tab"
      aria-selected={p.id === value}
      className={clsx(styles.chip, p.id === value && styles.chipActive)}
      onClick={() => onChange(p.id)}>
      <span className={styles.chipLabel}>{p.label}</span>
      <span className={styles.chipHint}>{p.hint}</span>
    </button>
  );
  return (
    <div className={styles.picker} role="tablist" aria-label="Your system">
      {groups.map(g => (
        <div key={g.name} className={styles.pickerGroup}>
          <span className={styles.pickerGroupName}>{g.name}</span>
          <div className={styles.chips}>
            {platforms
              .filter(p => (g.family === 'linux' ? p.family === 'linux' : p.family !== 'linux'))
              .map(render)}
          </div>
        </div>
      ))}
    </div>
  );
}

function Steps({platform}: {platform: Platform}) {
  return (
    <div role="tabpanel" aria-label={platform.label}>
      {platform.before && <p className={styles.before}>{platform.before}</p>}
      <ol className={styles.steps}>
        {platform.steps.map((s, i) => (
          <li key={s.title} className={styles.step}>
            <div className={styles.stepHead}>
              <span className={styles.stepNum} aria-hidden>
                {i + 1}
              </span>
              <Heading as="h3" className={styles.stepTitle}>
                {s.title}
              </Heading>
              <span className={styles.stepTime}>{s.time}</span>
            </div>
            {s.body && <p className={styles.stepBody}>{s.body}</p>}
            <CodeBlock language={s.lang}>{s.code}</CodeBlock>
          </li>
        ))}
      </ol>
      {platform.manual && (
        <details className={styles.details}>
          <summary>{platform.manual.title}</summary>
          <CodeBlock language={platform.manual.lang}>{platform.manual.code}</CodeBlock>
          {platform.family === 'linux' && platform.id !== 'linux' && (
            <p className={styles.small}>
              Signing key fingerprint <code>C910 E8A2 2680 CCCB BB7E 6ED2 F0DC FE8A 242B 1E99</code>. From then on
              your system&apos;s upgrades keep nyxd current.
            </p>
          )}
        </details>
      )}
      <details className={styles.details}>
        <summary>Installer options</summary>
        {platform.family === 'windows' ? (
          <CodeBlock language="powershell">{`# Download first to pass options
irm https://pkgs.nyxd.io/install.ps1 -OutFile install.ps1
.\\install.ps1 -Version v1.0.0     # a given release
.\\install.ps1 -Uninstall          # remove nyx; add -Purge to delete the distro too`}</CodeBlock>
        ) : (
          <CodeBlock language="bash">{`curl -fsSL https://pkgs.nyxd.io/install.sh | sh -s -- --version v1.0.0   # a given release
curl -fsSL https://pkgs.nyxd.io/install.sh | sh -s -- --no-start         # set up, do not start
curl -fsSL https://pkgs.nyxd.io/install.sh | sh -s -- --uninstall        # remove (add --purge for data)`}</CodeBlock>
        )}
        <p className={styles.small}>
          Every option is in the <Link to="/docs/getting-started/install">install guide</Link>.
        </p>
      </details>
    </div>
  );
}

const next = [
  {title: 'Compose stacks', body: 'Run multi-service apps from a compose.yaml.', to: '/docs/getting-started/usage#compose-stacks-nyx-compose'},
  {title: 'Build images', body: 'nyx build: Dockerfiles without Docker or BuildKit.', to: '/docs/getting-started/build'},
  {title: 'Rootless', body: 'Run the daemon as your own user.', to: '/docs/operations/rootless'},
  {title: 'HTTP API', body: 'Everything nyx does, over a Unix socket.', to: '/docs/reference/api-reference'},
];

/* ─── Page ──────────────────────────────────────────────────────────────── */

export default function GetStarted(): ReactNode {
  const [id, setId] = useState('ubuntu');
  useEffect(() => setId(detect()), []);
  const choose = (v: string) => {
    setId(v);
    window.history.replaceState(null, '', `#${v}`);
  };
  const platform = platforms.find(p => p.id === id) ?? platforms[0];

  return (
    <Layout title="Get started" description="Install nyxd and run your first container in under 60 seconds, on Linux, macOS or Windows.">
      <main className={styles.page}>
        <div className={clsx('container', styles.inner)}>
          <header className={styles.header}>
            <span className={styles.badge}>Under 60 seconds</span>
            <Heading as="h1" className={styles.title}>
              Get started with nyxd
            </Heading>
            <p className={styles.lead}>
              Pick your system, then run three steps: install, check, and run a container.
            </p>
          </header>

          <Picker value={id} onChange={choose} />
          <Steps platform={platform} />

          <section className={styles.next}>
            <Heading as="h2" className={styles.nextTitle}>
              Next
            </Heading>
            <div className={styles.nextGrid}>
              {next.map(n => (
                <Link key={n.title} to={n.to} className={styles.nextCard}>
                  <strong>{n.title}</strong>
                  <span>{n.body}</span>
                </Link>
              ))}
            </div>
          </section>
        </div>
      </main>
    </Layout>
  );
}
