// Terminal DEMO: un "shell" mínimo que responde unos pocos comandos. Nunca ejecuta nada real.
import { BaseTermSession, decodeTermFrame } from "../terminal";
import type { Terminals, TermSession } from "../terminal";

const enc = new TextEncoder();
const dec = new TextDecoder();
const PROMPT = "\x1b[1;34madmin@tango\x1b[0m:\x1b[1;36m~\x1b[0m$ ";

const replies: Record<string, string> = {
  help: "Comandos de la demo: help, uname -a, uptime, df -h, lsblk, cat /proc/mdstat, whoami, clear, exit",
  "uname -a": "Linux tango 6.12.111-amd64 #1 SMP PREEMPT_DYNAMIC Debian 6.12.111 x86_64 GNU/Linux",
  uptime: " 21:58:04 up 12 days,  4:17,  1 user,  load average: 0.62, 0.48, 0.41",
  whoami: "admin",
  "df -h":
    "Filesystem      Size  Used Avail Use% Mounted on\n/dev/sda2        20G  2.1G   18G  11% /\n/dev/md127      7.3T  4.6T  2.8T  62% /volume1",
  lsblk:
    "NAME     SIZE TYPE  MOUNTPOINTS\nnvme0n1  466G disk\nsda      7.3T disk\n└─md127  7.3T raid1 /volume1\nsdb      7.3T disk\n└─md127  7.3T raid1 /volume1\nsdc      3.6T disk",
  "cat /proc/mdstat":
    "Personalities : [raid1]\nmd127 : active raid1 sdb[1] sda[0]\n      7813894464 blocks super 1.2 [2/2] [UU]\n\nunused devices: <none>",
};

export class MockTerminals implements Terminals {
  private next = 1;

  async open(): Promise<TermSession> {
    const id = this.next++;
    let line = "";
    // eslint-disable-next-line prefer-const
    let s: BaseTermSession;
    const out = (t: string) => setTimeout(() => s.push(enc.encode(t)), 0);
    s = new BaseTermSession(id, {
      send: (frame) => {
        const f = decodeTermFrame(frame);
        if (!f) return;
        for (const ch of dec.decode(f.data)) {
          if (ch === "\r") {
            const cmd = line.trim();
            line = "";
            if (cmd === "exit") {
              out("\r\nlogout\r\n");
              setTimeout(() => s.exit("exit"), 50);
              return;
            }
            if (cmd === "clear") {
              out("\x1b[2J\x1b[H" + PROMPT);
              continue;
            }
            const r = cmd ? (replies[cmd] ?? `${cmd.split(" ")[0]}: comando no disponible en la demo (probá «help»)`) : "";
            out("\r\n" + (r ? r.replace(/\n/g, "\r\n") + "\r\n" : "") + PROMPT);
          } else if (ch === "\x7f") {
            if (line) {
              line = line.slice(0, -1);
              out("\b \b");
            }
          } else if (ch >= " ") {
            line += ch;
            out(ch);
          }
        }
      },
      resize: () => undefined,
      close: () => undefined,
    });
    out("\x1b[2mTerminal DEMO de TangoOS: no ejecuta comandos reales.\x1b[0m\r\n\r\n" + PROMPT);
    return s;
  }
}
