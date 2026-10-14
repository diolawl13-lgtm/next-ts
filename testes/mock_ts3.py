"""TeamSpeak 3 ClientQuery simulado, para testar o Next TS sem o TS de verdade.

Imita o protocolo: banner com "\\n\\r", auth por API Key, escapes, notificações
misturadas com respostas, abas sem servidor, mensagens privadas e resposta do bot.
"""
import socket
import threading
import time

ESC = [("\\", "\\\\"), ("/", "\\/"), (" ", "\\s"), ("|", "\\p"), ("\n", "\\n"), ("\r", "\\r"), ("\t", "\\t")]


def esc(s):
    for a, b in ESC:
        s = s.replace(a, b)
    return s


def unesc(s):
    out, i = [], 0
    m = {"\\": "\\", "/": "/", "s": " ", "p": "|", "n": "\n", "r": "\r", "t": "\t"}
    while i < len(s):
        if s[i] == "\\" and i + 1 < len(s):
            out.append(m.get(s[i + 1], s[i + 1])); i += 2
        else:
            out.append(s[i]); i += 1
    return "".join(out)


BANNER = ("TS3 Client\n\rWelcome to the TeamSpeak 3 ClientQuery interface, type \"help\" for a list of commands "
          "and \"help <command>\" for information on a specific command.\n\rUse the \"auth\" command to authenticate "
          "yourself. See \"help auth\" for details.\n\rselected schandlerid=1\n\r")


class MockTS3:
    def __init__(self, port=25639, key="ABCD-1234-EFGH"):
        self.port, self.key = port, key
        self.lock = threading.Lock()
        self.description = ""
        self.description2 = ""        # canal "Claim MissClick Bot Test" (cid 15)
        self.second_bot = True        # "MissClick Bot Test" no servidor
        self.ts_connected = True      # aba 1 conectada ao servidor
        self.bot_clid = 7
        self.bot_present = True
        self.sent = []                # (tempo, alvo, mensagem)
        self.commands = []            # todos os comandos recebidos
        self.noise = True             # manda notificações não pedidas no meio das respostas
        self.reply_from_bot = True
        # Resposta do bot ao !next (como a do MissClick Bot Test de verdade).
        self.reply_tmpl = "Você entrou na posição 1 da fila de {code}. Tempo reservado: 2:00."
        self.slow_send = 0.0
        self.refuse_send = False
        self.conns = []
        self.srv = socket.socket()
        self.srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.srv.bind(("127.0.0.1", port))
        self.srv.listen(8)
        self.alive = True
        threading.Thread(target=self._accept, daemon=True).start()

    def set_desc(self, text):
        with self.lock:
            self.description = text

    def drop_all(self):
        with self.lock:
            for c in self.conns:
                try: c.shutdown(socket.SHUT_RDWR); c.close()
                except OSError: pass
            self.conns = []

    def stop_listening(self):
        self.alive = False
        try: self.srv.shutdown(socket.SHUT_RDWR)
        except OSError: pass
        try: self.srv.close()
        except OSError: pass

    def _accept(self):
        while self.alive:
            try:
                c, _ = self.srv.accept()
            except OSError:
                return
            with self.lock:
                self.conns.append(c)
            threading.Thread(target=self._serve, args=(c,), daemon=True).start()

    def _send(self, c, text):
        try:
            c.sendall(text.encode("utf-8"))
        except OSError:
            pass

    def _serve(self, c):
        self._send(c, BANNER)
        authed, handler, buf = False, 1, b""
        while True:
            try:
                data = c.recv(4096)
            except OSError:
                return
            if not data:
                return
            buf += data
            while b"\n" in buf:
                raw, buf = buf.split(b"\n", 1)
                line = raw.decode("utf-8").strip("\r")
                if not line:
                    continue
                with self.lock:
                    self.commands.append(line)
                parts = line.split(" ")
                cmd, args = parts[0], {}
                flags = []
                for p in parts[1:]:
                    if "=" in p:
                        k, v = p.split("=", 1); args[k] = unesc(v)
                    else:
                        flags.append(p)
                out = ""
                if self.noise:
                    out += "notifytalkstatuschange schandlerid=1 status=0 isreceivedwhisper=0 clid=9\n\r"
                if cmd == "auth":
                    if args.get("apikey") == self.key:
                        authed = True; out += "error id=0 msg=ok\n\r"
                    else:
                        out += "error id=256 msg=invalid\\sapikey\n\r"
                elif not authed:
                    out += "error id=1796 msg=currently\\snot\\spossible\n\r"
                elif cmd == "serverconnectionhandlerlist":
                    out += "schandlerid=1|schandlerid=2\n\rerror id=0 msg=ok\n\r"
                elif cmd == "use":
                    handler = int(args.get("schandlerid", handler))
                    out += f"selected schandlerid={handler}\n\rerror id=0 msg=ok\n\r"
                elif cmd == "whoami":
                    if handler == 1 and self.ts_connected:
                        out += "clid=5 cid=30\n\rerror id=0 msg=ok\n\r"
                    else:
                        out += "error id=1794 msg=not\\sconnected\n\r"
                elif cmd == "servervariable":
                    out += "virtualserver_name=Missclick\n\rerror id=0 msg=ok\n\r" if handler == 1 else "error id=1794 msg=not\\sconnected\n\r"
                elif cmd == "clientvariable":
                    out += f"clid={args.get('clid')} client_nickname=Real\\sCalvon\n\rerror id=0 msg=ok\n\r"
                elif cmd == "channellist":
                    chans = [(1, "[cspacer]✦ x3tBot Channels ✦"), (5, "Claimed TEST"), (10, "x3tBot Commands"),
                             (11, "Friends | 89 |"), (12, "Claimeds"), (13, "Death List History"),
                             (14, "✦ REPORTAR ERRO CLAIMED ✦"), (15, "Claim MissClick Bot Test"), (30, "Sala 1")]
                    out += "|".join(f"cid={a} pid=0 channel_order=0 channel_name={esc(b)} total_clients=0" for a, b in chans)
                    out += "\n\rerror id=0 msg=ok\n\r"
                elif cmd == "clientlist":
                    rows = [(5, "Real Calvon", "MEUID="), (9, "Weepy", "WEEPY="), (11, "smoothyz", "SMOO=")]
                    if self.bot_present:
                        rows.append((self.bot_clid, "x3tBot Kalibra", "BOTUID+abc/="))
                    if self.second_bot:
                        rows.append((21, "MissClick Bot Test", "MCBOT/xyz="))
                    out += "|".join(f"clid={a} cid=30 client_database_id=1 client_nickname={esc(b)} client_type=0 client_unique_identifier={esc(u)}" for a, b, u in rows)
                    out += "\n\rerror id=0 msg=ok\n\r"
                elif cmd == "channelvariable":
                    cid = args.get("cid")
                    with self.lock:
                        d = {"12": self.description, "15": self.description2, "5": "Canal de testes"}.get(cid, "")
                    out += f"cid={cid} channel_description={esc(d)}\n\rerror id=0 msg=ok\n\r"
                elif cmd == "clientnotifyregister":
                    out += "error id=0 msg=ok\n\r"
                elif cmd == "sendtextmessage":
                    tgt = int(args.get("target", 0)); msg = args.get("msg", "")
                    if self.slow_send:
                        time.sleep(self.slow_send)
                    if self.refuse_send:
                        out += "error id=2568 msg=insufficient\\sclient\\spermissions\n\r"
                    elif not ((tgt == self.bot_clid and self.bot_present) or (tgt == 21 and self.second_bot)):
                        out += "error id=512 msg=invalid\\sclientID\n\r"
                    else:
                        with self.lock:
                            self.sent.append((time.time(), tgt, msg))
                        out += "error id=0 msg=ok\n\r"
                        # O próprio TS ecoa a mensagem enviada; depois o bot responde.
                        echo = f"notifytextmessage schandlerid=1 targetmode=1 msg={esc(msg)} target={tgt} invokerid=5 invokername=Real\\sCalvon invokeruid=MEUID=\n\r"
                        threading.Timer(0.05, self._send, (c, echo)).start()
                        if self.reply_from_bot:
                            bname, buid = ("MissClick\\sBot\\sTest", "MCBOT\\/xyz=") if tgt == 21 else ("x3tBot\\sKalibra", "BOTUID+abc\\/=")
                            rep = f"notifytextmessage schandlerid=1 targetmode=1 msg={esc('[b]' + self.reply_tmpl.format(code=msg.split(' ')[-1]) + '[/b]')} target=5 invokerid={tgt} invokername={bname} invokeruid={buid}\n\r"
                            threading.Timer(0.3, self._send, (c, rep)).start()
                else:
                    out += "error id=256 msg=command\\snot\\sfound\n\r"
                self._send(c, out)
