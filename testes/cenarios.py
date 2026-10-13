"""Testes de ponta a ponta: app real (versão Linux) + TeamSpeak simulado.

Uso: python3 -I cenarios.py <binario> [filtro]
"""
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import traceback
import urllib.request
import urllib.error

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from mock_ts3 import MockTS3  # noqa: E402

BIN = sys.argv[1]
FILTRO = sys.argv[2] if len(sys.argv) > 2 else ""
KEY = "ABCD-1234-EFGH"
mock = MockTS3(25639, KEY)


def desc(*lines, last="2026-10-06 18:02:13"):
    body = "\n".join(f"[b]{l}[/b]" for l in lines)
    return (f"[center][img]https://x3t.example/claimed.png[/img][/center]\n"
            f"[i]Last update: {last} America/Sao_Paulo[/i]\n\n{body}")


OCUP0 = "c1 - [01:30] Livraria de Fire (Tier 3): [color=blue]Kira Zinha[/color]"
OCUP1 = OCUP0 + " | Next: [color=blue]Lord Calvon[/color]"
OCUP2 = OCUP0 + " | Next: [color=blue]Lord Calvon[/color] +1"
OUTRAS = ["a1 - [00:22] Cobra Castelo (Tier 1): Jairo Slayer | Next: Lord Tenchi",
          "d15 - [00:35] Caminho Ferumbras (Tier 1): Guiixd",
          "c10 - [00:10] Teste: Fulano"]


def L(code="c1", t="01:30", who="Kira Zinha", nxt=(), resp="Livraria de Fire (Tier 3)", tag=True, sp=False):
    """Monta uma linha no formato novo do x3tBot. nxt = nomes no next. sp = tempo com espaços: "[ 01:30 ]" (13/10)."""
    b = (lambda x: f"[ {x} ]") if sp else (lambda x: f"[{x}]")
    ln = f"{code} - {b(t)} {resp}: [color=blue]{who}[/color]"
    for i, n in enumerate(nxt):
        ln += f" | Next: [color=blue]{n}[/color]" + (" " + b(f"Fim em ~{t}") if tag else "")
    return ln


def ACEITAR(code="C1", who="Lord Calvon", resp="Livraria de Fire (Tier 3)"):
    """Quem caçava saiu e o 1º da fila está aceitando o respawn (13/10)."""
    return f"{code} - [ Aceitar 00:03 ] {resp}: Next: [color=blue]{who}[/color] — aguardando aceitação"


class App:
    def __init__(self, data=None, auto="300ms", clock=""):
        self.data = data or tempfile.mkdtemp(prefix="nextts-")
        # "Janela do TeamSpeak" de mentira para a gravação (o texto vira o conteúdo da janela).
        self.tswin = os.path.join(self.data, "janela_ts.txt")
        if not os.path.exists(self.tswin):
            self.set_tswin("Chat privado com x3tBot Kalibra")
        env = dict(os.environ, NEXTTS_DATA=self.data, NEXTTS_NO_BROWSER="1", NEXTTS_AUTO_EVERY=auto, NEXTTS_FAKE_TS=self.tswin)
        if clock:
            env["NEXTTS_TEST_CLOCK"] = clock
        self.p = subprocess.Popen([BIN], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        line = self.p.stdout.readline()
        m = re.search(r"http://127\.0\.0\.1:(\d+)/\?t=(\w+)", line)
        assert m, "app não informou a URL: " + line
        self.port, self.token = int(m.group(1)), m.group(2)
        self.base = f"http://127.0.0.1:{self.port}"

    def api(self, path, body=None, token=None, host=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + "/api/" + path, data=data)
        req.add_header("X-Token", self.token if token is None else token)
        if host:
            req.add_header("Host", host)
        if body is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=10) as r:
                return r.status, json.loads(r.read() or b"null")
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                return e.code, json.loads(raw)
            except Exception:
                return e.code, raw.decode(errors="replace")

    def state(self):
        return self.api("state")[1]

    def connect(self, key=KEY, remember=True):
        return self.api("connect", {"key": key, "port": 25639, "remember": remember})

    def set_tswin(self, text):
        tmp = self.tswin + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            f.write(text)
        os.replace(tmp, self.tswin)

    def start(self, code="C1", codeB="", win=None, test=False, wait=False, rec=False):
        s = self.state()
        body = {"handler": s["handler"], "cid": s["cid"], "botUID": s["botUID"], "code": code, "codeB": codeB,
                "windowOn": bool(win), "windowStart": win[0] if win else "", "windowEnd": win[1] if win else "",
                "testMode": test, "waitGiveUp": wait, "record": rec}
        return self.api("start", body)

    def videos(self):
        d = os.path.join(self.data, "gravacoes")
        return sorted(os.path.join(d, f) for f in os.listdir(d)) if os.path.isdir(d) else []

    def mon(self):
        return self.state()["monitor"]

    def close(self):
        if self.p.poll() is None:
            self.p.terminate()
            try:
                self.p.wait(5)
            except subprocess.TimeoutExpired:
                self.p.kill()


def wait(cond, timeout, step=0.05):
    end = time.time() + timeout
    while time.time() < end:
        v = cond()
        if v:
            return v
        time.sleep(step)
    return None


def reset_mock():
    with mock.lock:
        mock.sent.clear(); mock.commands.clear()
    mock.ts_connected = True; mock.bot_clid = 7; mock.bot_present = True
    mock.reply_from_bot = True; mock.slow_send = 0; mock.refuse_send = False; mock.noise = True
    mock.key = KEY; mock.second_bot = True; mock.description2 = ""
    mock.set_desc(desc(*OUTRAS))


def logs(app):
    return [e["text"] for e in app.state()["log"]]


def delay_logged(app):
    for t in logs(app):
        m = re.search(r"Enviando o 1º next em (\d+[.,]\d) s", t)
        if m:
            return float(m.group(1).replace(",", "."))
    return None


RESULTS = []
# Cenários que precisam da conexão automática desligada (o app só conecta quando pedido).
AUTO = {"t25_reconectar_sem_colar_chave": "1h"}
# Cenários com relógio simulado (hora do "Windows" para o app).
CLOCK = {"t71": "19:26:00", "t72": "18:30:00", "t65": "18:30:00", "t52": "18:30:00", "t40": "18:30:00", "t41": "18:30:00", "t42": "18:30:00", "t43": "22:30:57", "t44": "23:00:00",
         "t45": "23:50:00", "t46": "18:30:20", "t47": "18:30:00", "t16": "18:30:00", "t18": "18:30:00"}


def scenario(fn):
    if FILTRO and FILTRO not in fn.__name__:
        return fn
    reset_mock()
    app = App(auto=AUTO.get(fn.__name__, "300ms"), clock=CLOCK.get(fn.__name__[:3], ""))
    t0 = time.time()
    try:
        fn(app)
        RESULTS.append((fn.__name__, True, f"{time.time() - t0:.1f}s"))
        print(f"OK    {fn.__name__} ({time.time() - t0:.1f}s)", flush=True)
    except Exception as e:
        RESULTS.append((fn.__name__, False, str(e)))
        print(f"FALHA {fn.__name__}: {e}", flush=True)
        traceback.print_exc()
        try:
            print("  log do app:", logs(app)[-12:])
        except Exception:
            pass
    finally:
        app.close()
        shutil.rmtree(app.data, ignore_errors=True)
    return fn


def connected(app):
    st, r = app.connect()
    assert st == 200, f"conectar: {st} {r}"


# ---------------------------------------------------------------- cenários

def entra(lines_before=(), **kw):
    """Coloca alguém novo na hunt do C1 (por padrão sem ninguém no next)."""
    mock.set_desc(desc(L(**kw), *OUTRAS, *lines_before))


@scenario
def t01_chave_errada_e_certa(app):
    st, r = app.connect("CHAVE-ERRADA")
    assert st == 400 and "recusou" in r["error"], r
    assert not app.state()["connected"]
    connected(app)
    s = app.state()
    assert s["connected"] and s["handler"] == 1 and len(s["servers"]) == 1, s["servers"]
    assert s["cid"] == 12, "deveria escolher o canal Claimeds sozinho"
    assert s["botUID"] == "BOTUID+abc/=", "deveria escolher o x3tBot sozinho"
    assert s["servers"][0]["myName"] == "Real Calvon" and s["servers"][0]["name"] == "Missclick"


@scenario
def t02_ts_fechado(app):
    mock.stop_listening()
    try:
        st, r = app.connect()
        assert st == 400 and "TeamSpeak" in r["error"], r
    finally:
        time.sleep(0.2)
        globals()["mock"] = MockTS3(25639, KEY)
        reset_mock()


@scenario
def t03_quem_ja_estava_sem_next_pega(app):
    # 1.4.2 ("sempre pegar"): Kira já está na hunt ao Iniciar e ninguém no next: pega na hora.
    connected(app)
    mock.set_desc(desc(OCUP0, *OUTRAS))
    t = time.time()
    st, r = app.start("c1")
    assert st == 200, r
    assert wait(lambda: mock.sent, 5), "não pegou quem já estava na hunt sem next"
    dt = mock.sent[0][0] - t
    assert 0.95 <= dt <= 2.9, f"atraso fora do esperado: {dt:.2f}s"
    assert mock.sent[0][1] == 7 and mock.sent[0][2] == "!next C1", mock.sent
    assert any("Kira Zinha já está caçando em C1 (Plano A) e ninguém está no next" in x for x in logs(app)), logs(app)
    assert wait(lambda: any("respondeu" in x for x in logs(app)), 2), "resposta do bot não apareceu"
    s = app.mon()
    assert not s["running"] and s["phase"] == "sent", s
    time.sleep(3)
    assert len(mock.sent) == 1, "enviou mais de uma vez"
    assert not any("Real Calvon respondeu" in x for x in logs(app)), "mostrou a própria mensagem como resposta"
@scenario
def t04_livre_nunca_envia_depois_alguem_entra(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))  # C1 livre
    app.start("C1")
    time.sleep(3)
    assert not mock.sent, "enviou com o respawn livre!"
    assert "livre" in app.mon()["status"].lower()
    t = time.time()
    mock.set_desc(desc(OCUP0, *OUTRAS))
    assert wait(lambda: mock.sent, 5), "não enviou quando alguém entrou"
    dt = mock.sent[0][0] - t
    assert 0.95 <= dt <= 2.7, f"atraso {dt:.2f}s"
    d = delay_logged(app)
    assert d and 1.0 <= d <= 2.0, d


@scenario
def t05_entrou_ja_com_next_falha(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    mock.set_desc(desc(OCUP1, *OUTRAS))  # entrou já com alguém no next
    assert wait(lambda: app.mon()["phase"] == "failed", 3), app.mon()
    assert not mock.sent
    m = app.mon()
    assert "Lord Calvon" in m["status"] and "Nada foi enviado" in m["status"], m["status"]
    mock.set_desc(desc(OCUP0, *OUTRAS))
    time.sleep(2.5)
    assert not mock.sent, "continuou depois de falhar"


@scenario
def t06_pegaram_next_na_espera_vai_pro_B(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", "D7")
    time.sleep(1)
    entra()  # alguém entra no C1
    assert wait(lambda: app.mon()["phase"] == "pending", 2), "não entrou em espera"
    time.sleep(0.3)
    entra(nxt=["Fulano Rapido"])  # outra pessoa pegou o next antes
    assert wait(lambda: app.mon()["plan"] == "B", 3), app.mon()
    m = app.mon()
    assert m["code"] == "D7" and m["running"], m
    assert any("Plano A (C1) falhou" in x and "Fulano Rapido" in x for x in logs(app)), logs(app)
    time.sleep(2.5)
    assert not mock.sent, "enviou no Plano A depois de falhar"
    # Alguém entra no D7: pega o D7.
    mock.set_desc(desc(L(nxt=["Fulano Rapido"]), L("D7", "02:00", "Elite Nitrox", resp="DT -1 (Tier 1)"), *OUTRAS))
    assert wait(lambda: mock.sent, 5), "Plano B não enviou"
    assert mock.sent[0][2] == "!next D7", mock.sent
    time.sleep(2.5)
    assert len(mock.sent) == 1


@scenario
def t07_nao_volta_para_o_A(app):
    connected(app)
    d7 = L("D7", "01:00", "Ja Estava", resp="DT", nxt=["Alguem"])  # já caçando, com next: não pega
    mock.set_desc(desc(d7, *OUTRAS))
    app.start("C1", "D7")
    time.sleep(1)
    mock.set_desc(desc(L(nxt=["Apressado"]), d7, *OUTRAS))  # entrou com next: A falha
    assert wait(lambda: app.mon()["plan"] == "B", 3)
    mock.set_desc(desc(L(who="Outro Cara"), d7, *OUTRAS))   # A abre de novo: não pode mais pegar o A
    time.sleep(3.5)
    assert not mock.sent, "voltou para o Plano A ou pegou quem já estava no B com next"
    mock.set_desc(desc(L(who="Outro Cara"), L("D7", "02:00", "Novo Hunter", resp="DT"), *OUTRAS))
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next D7"
@scenario
def t08_modo_teste(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", test=True)
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "tested", 5), app.mon()
    time.sleep(1)
    assert not mock.sent, "modo teste enviou!"
    assert not any(c.startswith("sendtextmessage") for c in mock.commands)
    assert any("MODO TESTE" in x for x in logs(app))


@scenario
def t09_parar_durante_espera(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "pending", 2)
    time.sleep(0.3)
    st, _ = app.api("stop", {})
    assert st == 200
    time.sleep(3)
    assert not mock.sent, "enviou depois de clicar em Parar"
    s = app.mon()
    assert not s["running"] and s["phase"] == "stopped"


@scenario
def t10_ts_desconectado_do_servidor(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    mock.ts_connected = False
    mock.set_desc(desc(OCUP0, *OUTRAS))  # lista antiga em cache não pode valer
    time.sleep(3)
    assert not mock.sent, "enviou com o TS desconectado do servidor"
    assert app.mon()["phase"] == "waitingts"
    mock.ts_connected = True
    assert wait(lambda: mock.sent, 5), "não voltou a funcionar"


@scenario
def t11_conexao_cai_e_volta(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    mock.drop_all()
    assert wait(lambda: any("Reconectado" in x for x in logs(app)), 8), logs(app)
    mock.set_desc(desc(OCUP0, *OUTRAS))
    assert wait(lambda: mock.sent, 6), "não enviou depois de reconectar"
    assert len(mock.sent) == 1


@scenario
def t12_bot_muda_de_numero(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    mock.bot_clid = 42  # o bot reconectou e ganhou outro número
    time.sleep(1)
    mock.set_desc(desc(OCUP0, *OUTRAS))
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][1] == 42, mock.sent


@scenario
def t13_bot_fora_do_servidor(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    mock.bot_present = False
    app.start("C1")
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "error", 5)
    assert not mock.sent
    assert "não está no servidor" in app.mon()["status"]


@scenario
def t14_ts_recusa_envio(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    mock.refuse_send = True
    app.start("C1")
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "error", 5)
    time.sleep(2)
    n = sum(c.startswith("sendtextmessage") for c in mock.commands)
    assert n == 1, f"tentou enviar {n} vezes"


@scenario
def t15_codigos_parecidos(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("D1")
    time.sleep(1)
    mock.set_desc(desc("d15 - [00:35] Caminho Ferumbras (Tier 1): Outro", "c10 - [00:10] Teste: Novo",
                       "ac1 - [00:10] Outro: Fulano"))
    time.sleep(3)
    app.api("stop", {})
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    mock.set_desc(desc("c10 - [00:10] Teste: Novo2", "ac1 - [00:10] Outro: Fulano2"))
    time.sleep(3)
    assert not mock.sent, "confundiu D1 com D15 ou C1 com C10/AC1"


@scenario
def t16_preferencias_e_chave_salva(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    st, r = app.start("D7", "a1", win=("20:00", "22:15"))
    assert st == 200, r
    time.sleep(0.5)
    app.api("stop", {})
    app.close()
    raw = open(os.path.join(app.data, "preferencias.json")).read()
    assert KEY not in raw, "a chave ficou em texto puro"
    app2 = App(app.data)
    try:
        assert wait(lambda: app2.state()["connected"], 6), "não reconectou com a chave salva"
        s = app2.state()
        assert s["code"] == "D7" and s["codeB"] == "A1" and s["windowOn"] is True, s
        assert s["windowStart"] == "20:00" and s["windowEnd"] == "22:15", s
        assert not s["monitor"]["running"], "não pode começar ligado sozinho"
    finally:
        app2.close()


@scenario
def t17_nao_lembrar_chave(app):
    st, r = app.connect(remember=False)
    assert st == 200
    app.close()
    prefs = json.load(open(os.path.join(app.data, "preferencias.json")))
    assert not prefs.get("keyProtected"), prefs


@scenario
def t18_validacoes(app):
    st, r = app.start("C1")
    assert st == 400, "iniciou sem conectar"
    connected(app)
    st, r = app.start("!next c1")
    assert st == 400 and "código" in r["error"].lower(), r
    st, r = app.start("C1", "c1")
    assert st == 400 and "mesmo respawn" in r["error"], r
    st, r = app.start("C1", "xx")
    assert st == 400 and "Plano B" in r["error"], r
    st, r = app.start("C1", win=("20", "22:30"))
    assert st == 400 and "inválido" in r["error"], r
    st, r = app.start("C1", win=("08:00", "09:00"))  # relógio simulado: 18:30
    assert st == 400 and "já acabou" in r["error"], r
    mock.set_desc(desc(*OUTRAS))
    assert app.start("C1")[0] == 200
    st, r = app.start("C1")
    assert st == 400, "iniciou duas vezes"
    st, r = app.connect()
    assert st == 400, "trocou conexão com o monitor ligado"


@scenario
def t19_seguranca_http(app):
    st, _ = app.api("state", token="errado")
    assert st == 403
    st, _ = app.api("state", host="localhost:%d" % app.port)
    assert st == 403
    req = urllib.request.Request(app.base + "/")
    try:
        urllib.request.urlopen(req)
        raise AssertionError("página abriu sem o código de acesso")
    except urllib.error.HTTPError as e:
        assert e.code == 403
    with urllib.request.urlopen(app.base + "/?t=" + app.token) as r:
        assert b"Next" in r.read()


@scenario
def t20_fechar_janela_encerra(app):
    urllib.request.urlopen(urllib.request.Request(app.base + "/api/bye?t=" + app.token, data=b"x"))
    time.sleep(0.3)
    app.state()
    time.sleep(6.5)
    assert app.p.poll() is None, "fechou ao recarregar"
    urllib.request.urlopen(urllib.request.Request(app.base + "/api/bye?t=" + app.token, data=b"x"))
    assert wait(lambda: app.p.poll() is not None, 9), "não fechou depois que a janela foi fechada"


@scenario
def t21_segunda_abertura(app):
    env = dict(os.environ, NEXTTS_DATA=app.data, NEXTTS_NO_BROWSER="1")
    p2 = subprocess.Popen([BIN], env=env, stdout=subprocess.PIPE, text=True)
    try:
        p2.wait(5)
    except subprocess.TimeoutExpired:
        p2.kill()
        raise AssertionError("segunda cópia não fechou")
    assert app.p.poll() is None and app.state()["version"]


@scenario
def t22_atrasos_aleatorios(app):
    connected(app)
    vals, logged = [], []
    for i in range(6):
        with mock.lock:
            mock.sent.clear()
        mock.set_desc(desc(*OUTRAS))
        app.start("C1")
        time.sleep(0.8)
        t = time.time()
        entra(who=f"Hunter {i}")
        assert wait(lambda: mock.sent, 5)
        vals.append(mock.sent[0][0] - t)
        logged.append(delay_logged(app))
        with mock.lock:
            pass
        app.state()
        time.sleep(0.3)
    print("    atrasos medidos (desde a mudança da lista):", [round(v, 2) for v in vals])
    assert all(0.95 <= v <= 2.7 for v in vals), vals
    assert max(vals) - min(vals) > 0.15, "atrasos iguais demais (não parecem aleatórios)"


@scenario
def t23_ritmo_de_leitura(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    with mock.lock:
        mock.commands.clear()
    app.start("C1")
    time.sleep(5.05)
    app.api("stop", {})
    with mock.lock:
        n = sum(c.startswith("channelvariable") for c in mock.commands)
    assert 9 <= n <= 12, f"{n} leituras em 5 s (esperado ~10, uma a cada 0,5 s)"


@scenario
def t24_envio_lento_nao_duplica(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    mock.slow_send = 1.0
    app.start("C1")
    time.sleep(1)
    entra()
    assert wait(lambda: mock.sent, 6)
    time.sleep(3)
    assert len(mock.sent) == 1
    assert app.mon()["phase"] == "sent"


@scenario
def t25_reconectar_sem_colar_chave(app):
    st, r = app.connect(remember=False)
    assert st == 200
    mock.drop_all()
    assert wait(lambda: not app.state()["connected"], 3), "não percebeu a queda"
    s = app.state()
    assert s["hasKey"] and not s["hasSavedKey"], s
    st, r = app.api("connect", {"key": "", "port": 25639, "remember": False})
    assert st == 200, r
    assert app.state()["connected"]


@scenario
def t26_canal_certo_mesmo_com_canal_de_teste(app):
    json.dump({"port": 25639, "remember": True, "channelName": "Claimed TEST", "code": "C1"},
              open(os.path.join(app.data, "preferencias.json"), "w"))
    app.close()
    app2 = App(app.data)
    try:
        st, r = app2.connect()
        assert st == 200, r
        assert app2.state()["cid"] == 12, "escolheu o canal errado: %s" % app2.state()["cid"]
        mock.set_desc(desc(OCUP2, *OUTRAS))
        st, pv = app2.api("peek", {"cid": 12, "code": "C1", "codeB": "A1"})
        assert pv["ok"] and pv["lines"] == 4 and "2 no next" in pv["summary"], pv
        assert "sai às" in pv["summary"] and "Jairo Slayer" in pv["summaryB"], pv
        st, pv = app2.api("peek", {"cid": 5, "code": "C1"})
        assert pv["ok"] and pv["lines"] == 0 and not pv["hasList"], pv
        s = app2.state()
        st, r = app2.api("start", {"handler": 1, "cid": 5, "botUID": s["botUID"], "code": "C1"})
        assert st == 400 and "sem a lista" in r["error"], r
        mock.set_desc(desc())
        st, pv = app2.api("peek", {"cid": 12, "code": "C1"})
        assert pv["hasList"] and pv["lines"] == 0 and "livre" in pv["summary"], pv
    finally:
        app2.close()


@scenario
def t27_versao_antiga_aberta_e_fechada(app):
    inst = json.load(open(os.path.join(app.data, "instancia.json")))
    assert inst["port"] == app.port
    import http.server, threading
    class Old(http.server.BaseHTTPRequestHandler):
        quit = False
        def do_GET(self):
            body = b'{"app":"nextts"}' if not Old.quit else b''
            self.send_response(200 if not Old.quit else 404); self.end_headers(); self.wfile.write(body)
        def do_POST(self):
            Old.quit = True; self.send_response(200); self.end_headers(); self.wfile.write(b'{"ok":true}')
        def log_message(self, *a): pass
    srv = http.server.HTTPServer(("127.0.0.1", 0), Old)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    app.close()
    json.dump({"port": srv.server_address[1], "token": "x"}, open(os.path.join(app.data, "instancia.json"), "w"))
    app2 = App(app.data)
    try:
        assert Old.quit, "não pediu para a versão antiga fechar"
        assert app2.state()["version"]
    finally:
        app2.close(); srv.shutdown()


@scenario
def t28_formato_novo_e_antigo(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    # Formato novo, entra com 2 no next: falha (pegaram antes).
    mock.set_desc(desc(L(nxt=["Shawn Shooter", "Billy Noval"]), *OUTRAS))
    assert wait(lambda: app.mon()["phase"] == "failed", 3), app.mon()
    assert app.mon()["count"] == 2
    assert not mock.sent
    # Formato antigo com "/": entrou com 1 vaga no next também falha.
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    mock.set_desc(desc(OCUP0 + " | Next: Dudu / Pedrinho", *OUTRAS))
    assert wait(lambda: app.mon()["phase"] == "failed", 3), app.mon()
    # Formato novo em maiúsculas, sem Tier, sem ninguém no next: pega.
    mock.set_desc(desc(*OUTRAS))
    app.start("B3")
    time.sleep(1)
    mock.set_desc(desc("B3 - [01:16] Gloom Pillars: Arthy Zephyr", *OUTRAS))
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next B3"


@scenario
def t29_atalhos_x3t_e_missclick(app):
    mock.description2 = desc()
    connected(app)
    s = app.state()
    names = {p["name"]: p for p in s["presets"]}
    assert "x3tBot" in names and names["x3tBot"]["cid"] == 12 and names["x3tBot"]["botUID"] == "BOTUID+abc/=", s["presets"]
    mc = names.get("MissClick Bot Test")
    assert mc and mc["cid"] == 15 and mc["botUID"] == "MCBOT/xyz=", s["presets"]
    assert all(p["botOnline"] for p in s["presets"]), s["presets"]
    st, r = app.api("select", {"cid": 15, "botUID": "MCBOT/xyz="})
    assert st == 200, r
    s = app.state()
    assert s["cid"] == 15 and s["botUID"] == "MCBOT/xyz=", (s["cid"], s["botUID"])
    st, r = app.api("start", {"handler": 1, "cid": 15, "botUID": "MCBOT/xyz=", "code": "C1"})
    assert st == 200, r
    time.sleep(1)
    mock.description2 = desc("c1 - [01:00] Livraria de Fire (Tier 3): Fulano Teste")
    assert wait(lambda: mock.sent, 5), "não enviou pelo atalho do MissClick"
    assert mock.sent[0][1] == 21 and mock.sent[0][2] == "!next C1", mock.sent
    assert wait(lambda: any("MissClick Bot Test respondeu" in x for x in logs(app)), 3), logs(app)
    app.close()
    app2 = App(app.data)
    try:
        assert wait(lambda: app2.state()["connected"], 6), "não conectou sozinho ao reabrir"
        s = app2.state()
        assert s["cid"] == 15 and s["botUID"] == "MCBOT/xyz=", (s["cid"], s["botUID"])
    finally:
        app2.close()


@scenario
def t30_escolha_lembrada_sem_iniciar(app):
    connected(app)
    st, r = app.api("select", {"cid": 5, "botUID": "MCBOT/xyz="})
    assert st == 200, r
    app.close()
    app2 = App(app.data)
    try:
        assert wait(lambda: app2.state()["connected"], 6)
        s = app2.state()
        assert s["cid"] == 5 and s["botUID"] == "MCBOT/xyz=", (s["cid"], s["botUID"])
    finally:
        app2.close()


@scenario
def t31_conecta_sozinho_quando_o_ts_abre(app):
    connected(app)
    app.close()
    mock.stop_listening()
    app2 = App(app.data)
    try:
        time.sleep(2)
        s = app2.state()
        assert not s["connected"] and s["autoWaiting"], s
        avisos = sum("Vou conectar sozinho" in x for x in logs(app2))
        assert avisos == 1, logs(app2)
        assert not any(x.startswith("Não conectou") for x in logs(app2)), logs(app2)
        globals()["mock"] = MockTS3(25639, KEY)
        reset_mock()
        assert wait(lambda: app2.state()["connected"], 6), "não conectou sozinho quando o TS abriu"
        s = app2.state()
        assert s["cid"] == 12 and not s["autoWaiting"], s
    finally:
        app2.close()


@scenario
def t32_chave_recusada_para_de_tentar(app):
    connected(app)
    app.close()
    mock.key = "OUTRA-CHAVE"
    app2 = App(app.data)
    try:
        assert wait(lambda: app2.state()["autoBlocked"], 5), app2.state()
        s = app2.state()
        assert "recusou" in s["connErr"] and not s["connected"] and not s["autoWaiting"], s
        n1 = sum(c.startswith("auth") for c in mock.commands)
        time.sleep(1.5)
        n2 = sum(c.startswith("auth") for c in mock.commands)
        assert n1 == n2, f"continuou tentando com a chave recusada: {n1} -> {n2}"
        mock.key = KEY
        st, r = app2.connect()
        assert st == 200, r
        assert not app2.state()["autoBlocked"]
    finally:
        app2.close()
        mock.key = KEY


@scenario
def t33_ts_cai_parado_e_volta_sozinho(app):
    connected(app)
    mock.drop_all()
    assert wait(lambda: sum("Conectado ao TeamSpeak" in x for x in logs(app)) >= 2, 6), logs(app)
    assert app.state()["connected"]


@scenario
def t34_salvar_e_apagar_atalho(app):
    connected(app)
    st, r = app.api("preset/save", {"name": "Meu teste", "cid": 5, "botUID": "MCBOT/xyz="})
    assert st == 200, r
    st, r = app.api("preset/delete", {"name": "x3tBot"})
    assert st == 200, r
    st, r = app.api("refresh", {"handler": 1})
    assert st == 200, r
    names = [p["name"] for p in app.state()["presets"]]
    assert "Meu teste" in names and "x3tBot" not in names, names
    st, r = app.api("preset/save", {"name": "", "cid": 5, "botUID": "MCBOT/xyz="})
    assert st == 400, r
    st, r = app.api("preset/save", {"name": "X", "cid": 999, "botUID": "MCBOT/xyz="})
    assert st == 400, r


@scenario
def t35_parar_bem_na_hora_do_envio(app):
    connected(app)
    mock.slow_send = 2.0
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    entra()
    assert wait(lambda: any(c.startswith("sendtextmessage") for c in list(mock.commands)), 6), "não chegou a enviar"
    st, _ = app.api("stop", {})
    assert st == 200
    assert wait(lambda: app.mon()["phase"] == "sent", 6), app.mon()
    lg = logs(app)
    assert any("ENVIADO" in x for x in lg), lg
    assert any("já estava sendo enviado" in x for x in lg), lg
    assert not any("Nada foi enviado" in x for x in lg), lg
    assert len(mock.sent) == 1


@scenario
def t36_atalho_com_nome_repetido(app):
    connected(app)
    st, r = app.api("preset/save", {"name": "x3tBot", "cid": 5, "botUID": "BOTUID+abc/="})
    assert st == 200, r
    st, r = app.api("refresh", {"handler": 1})
    assert st == 200, r
    ps = [p for p in app.state()["presets"] if p["name"].lower() == "x3tbot"]
    assert len(ps) == 1 and ps[0]["cid"] == 5, app.state()["presets"]


@scenario
def t37_respawn_livre_na_espera_nao_falha(app):
    # A pessoa entrou e saiu antes do envio: não é falha, continua esperando no A.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", "D7")
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "pending", 2)
    mock.set_desc(desc(*OUTRAS))
    time.sleep(2.5)
    assert not mock.sent and app.mon()["plan"] == "A" and app.mon()["running"], app.mon()
    entra(who="Volta Logo")
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next C1"


@scenario
def t38_mesmo_hunter_renova_conta_como_entrada(app):
    # O tempo do mesmo caçador pulou para cima (fez claim de novo): é uma hunt nova.
    connected(app)
    mock.set_desc(desc(L(t="00:05", nxt=["Fulano"]), *OUTRAS))  # já caçando, com next: não pega
    app.start("C1")
    time.sleep(2)
    assert not mock.sent
    mock.set_desc(desc(L(t="00:04", nxt=["Fulano"]), *OUTRAS))  # tempo só diminuiu: nada
    time.sleep(1.5)
    assert not mock.sent and app.mon()["running"] and app.mon()["phase"] != "failed", app.mon()
    mock.set_desc(desc(L(t="02:30"), *OUTRAS))  # renovou (e o Fulano saiu da fila)
    assert wait(lambda: mock.sent, 5), "não pegou quando o caçador renovou"
@scenario
def t39_leitura_ruim_na_espera_nao_engana(app):
    # Linha estranha na hora de enviar: só envia se a conferência final der certo.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "pending", 2)
    mock.set_desc(desc(L() + " | Next:", *OUTRAS))  # linha quebrada
    time.sleep(3.5)
    assert not mock.sent, "enviou com a linha quebrada"
    entra()  # volta ao normal (mesmo caçador, sem next)
    assert wait(lambda: mock.sent, 4), "não enviou depois que a linha voltou ao normal"


@scenario
def t40_janela_dentro_envia(app):
    # Relógio 18:30. Entra alguém com 01:45 → hunt às 20:15, dentro de 20:00–22:30.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    st, r = app.start("C1", win=("20:00", "22:30"))
    assert st == 200, r
    assert app.mon()["window"] == "20:00 – 22:30", app.mon()
    time.sleep(1)
    entra(t="01:45")
    assert wait(lambda: mock.sent, 5), "não enviou dentro da janela"
    lg = " ".join(logs(app))
    assert "Agora 18:30 + falta 01:45 = sua hunt começaria às 20:15 → dentro da janela" in lg, lg
    assert app.state()["now"].startswith("18:3"), app.state()["now"]


@scenario
def t41_janela_fora_vai_pro_B_e_pega(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", "D7", win=("20:00", "22:30"))
    time.sleep(1)
    entra(t="01:20")  # 19:50: cedo demais
    assert wait(lambda: app.mon()["plan"] == "B", 3), app.mon()
    assert any("19:50" in x and "fora da janela" in x for x in logs(app)), logs(app)
    assert not mock.sent
    mock.set_desc(desc(L(t="01:20"), L("D7", "02:00", "Hunter B", resp="DT"), *OUTRAS))  # 20:30
    assert wait(lambda: mock.sent, 5), "Plano B não enviou"
    assert mock.sent[0][2] == "!next D7"


@scenario
def t42_janela_fora_B_tambem_para(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", "D7", win=("20:00", "22:30"))
    time.sleep(1)
    entra(t="05:00")  # 23:30: tarde demais
    assert wait(lambda: app.mon()["plan"] == "B", 3), app.mon()
    mock.set_desc(desc(L(t="05:00"), L("D7", "01:00", "Hunter B", resp="DT"), *OUTRAS))  # 19:30
    assert wait(lambda: app.mon()["phase"] == "failed", 3), app.mon()
    m = app.mon()
    assert "Plano B (D7) também falhou" in m["status"] and "Nada foi enviado" in m["status"], m["status"]
    time.sleep(2)
    assert not mock.sent


@scenario
def t43_janela_encerra_sozinha(app):
    # Relógio 22:30:57: em ~3 s passa das 22:30 e o app para sozinho.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    st, r = app.start("C1", win=("20:00", "22:30"))
    assert st == 200, r
    assert wait(lambda: app.mon()["phase"] == "ended", 6), app.mon()
    assert "Janela encerrada" in app.mon()["status"]
    assert not mock.sent


@scenario
def t44_janela_que_ja_acabou_nao_inicia(app):
    connected(app)
    st, r = app.start("C1", win=("20:00", "22:30"))  # relógio 23:00
    assert st == 400 and "já acabou" in r["error"], r
    st, r = app.start("C1", win=("23:30", "01:00"))  # passa da meia-noite: vale
    assert st == 200, r


@scenario
def t45_janela_meia_noite(app):
    # Relógio 23:50, janela 23:00–01:00.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", "D7", win=("23:00", "01:00"))
    time.sleep(1)
    entra(t="01:20")  # 01:10: fora
    assert wait(lambda: app.mon()["plan"] == "B", 3), app.mon()
    mock.set_desc(desc(L(t="01:20"), L("D7", "01:00", "Hunter B", resp="DT"), *OUTRAS))  # 00:50: dentro
    assert wait(lambda: mock.sent, 5), app.mon()
    assert mock.sent[0][2] == "!next D7"


@scenario
def t46_janela_ponta_incluida(app):
    # Relógio 18:30:20. [04:00] → 22:30 (na ponta: pega). [04:01] → 22:31 (fora).
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", win=("20:00", "22:30"))
    time.sleep(1)
    entra(t="04:01")
    assert wait(lambda: app.mon()["phase"] == "failed", 3), app.mon()
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", win=("20:00", "22:30"))
    time.sleep(1)
    entra(who="Na Ponta", t="04:00")
    assert wait(lambda: mock.sent, 5), app.mon()


@scenario
def t47_janela_sem_tempo_na_linha_nao_envia(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", win=("20:00", "22:30"))
    time.sleep(1)
    mock.set_desc(desc("c1 - Livraria de Fire (Tier 3): Sem Tempo", *OUTRAS))
    time.sleep(3)
    assert not mock.sent, "enviou sem conseguir conferir o horário"
    assert any("não consegui ler o tempo" in x for x in logs(app)), logs(app)
    assert app.mon()["running"] and app.mon()["plan"] == "A"


@scenario
def t48_linha_pisca_nao_conta_como_entrada(app):
    # A linha do C1 some por um instante e volta com a mesma pessoa: não é entrada.
    # (Se fosse, "entrou já com next" faria o plano falhar.)
    connected(app)
    mock.set_desc(desc(OCUP1, *OUTRAS))
    app.start("C1")
    time.sleep(1.5)
    mock.set_desc(desc(*OUTRAS))
    time.sleep(0.8)
    mock.set_desc(desc(OCUP1, *OUTRAS))
    time.sleep(3.5)
    m = app.mon()
    assert not mock.sent and m["running"] and m["phase"] != "failed", m
    assert not any("entrou" in x for x in logs(app)), logs(app)
    # Mas se ela sai e entra outra pessoa, aí pega.
    mock.set_desc(desc(*OUTRAS))
    time.sleep(0.8)
    entra(who="Gente Nova")
    assert wait(lambda: mock.sent, 5)

# ---------------------------------------------------------------- 1.4: esperar o next desistir

@scenario
def t49_espera_next_desistir_e_envia(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    st, r = app.start("C1", wait=True)
    assert st == 200, r
    assert app.mon()["waitGiveUp"] is True
    time.sleep(1)
    entra(nxt=["Lord Calvon"])  # entrou já com alguém no next: com a opção, NÃO falha
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3), app.mon()
    time.sleep(2.5)
    m = app.mon()
    assert m["running"] and m["plan"] == "A" and m["phase"] == "watching", m
    assert "Esperando o next desistir" in m["status"], m["status"]
    assert not mock.sent, "enviou com alguém no next"
    assert any("Vou esperar o next desistir" in x for x in logs(app)), logs(app)
    t = time.time()
    entra()  # Lord Calvon saiu da fila (desistiu)
    assert wait(lambda: mock.sent, 5), "não enviou quando o next desistiu"
    dt = mock.sent[0][0] - t
    assert 0.95 <= dt <= 2.7, f"atraso {dt:.2f}s"
    assert mock.sent[0][2] == "!next C1"
    assert any("Lord Calvon desistiu do next de C1" in x for x in logs(app)), logs(app)
    assert wait(lambda: app.mon()["phase"] == "sent", 2)
    time.sleep(2)
    assert len(mock.sent) == 1


@scenario
def t50_next_nao_desiste_e_entra_falha_vai_B(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", "D7", wait=True)
    time.sleep(1)
    entra(nxt=["Lord Calvon"])
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3), app.mon()
    # O tempo do Kira acabou e o next (Lord Calvon) entrou na hunt: não desistiu.
    entra(who="Lord Calvon", t="02:00")
    assert wait(lambda: app.mon()["plan"] == "B", 3), app.mon()
    assert any("Lord Calvon não desistiu do next e entrou na hunt" in x for x in logs(app)), logs(app)
    assert app.mon()["waitingFor"] == ""
    time.sleep(2.5)
    assert not mock.sent, "enviou depois que o A falhou"
    # B: alguém entra no D7 sem next → pega.
    mock.set_desc(desc(L(who="Lord Calvon", t="02:00"), L("D7", "01:00", "Hunter B", resp="DT"), *OUTRAS))
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next D7"


@scenario
def t51_dois_no_next_um_desiste_continua(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", wait=True)
    time.sleep(1)
    entra(nxt=["Shawn Shooter", "Billy Noval"])
    assert wait(lambda: app.mon()["waitingFor"] == "Shawn Shooter", 3), app.mon()
    entra(nxt=["Billy Noval"])  # só 1 desistiu: ainda tem gente na fila
    assert wait(lambda: app.mon()["waitingFor"] == "Billy Noval", 3), app.mon()
    time.sleep(2.5)
    assert not mock.sent, "enviou com 1 ainda no next"
    entra()  # o outro também desistiu
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next C1"


@scenario
def t52_espera_com_janela(app):
    # Relógio 18:30. A: entra com next e a hunt seria às 23:30 (fora): falha na hora, nem espera.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", "D7", win=("20:00", "22:30"), wait=True)
    time.sleep(1)
    entra(t="05:00", nxt=["Atrasado"])
    assert wait(lambda: app.mon()["plan"] == "B", 3), app.mon()
    assert any("23:30" in x and "fora da janela" in x for x in logs(app)), logs(app)
    # B: entra com next e a hunt seria às 20:15 (dentro): espera; o next desiste: envia.
    d7 = lambda nxt: L("D7", "01:45", "Hunter B", resp="DT", nxt=nxt)
    mock.set_desc(desc(L(t="05:00", nxt=["Atrasado"]), d7(["Fulano"]), *OUTRAS))
    assert wait(lambda: app.mon()["waitingFor"] == "Fulano", 3), app.mon()
    time.sleep(1)
    assert not mock.sent
    mock.set_desc(desc(L(t="05:00", nxt=["Atrasado"]), d7([]), *OUTRAS))
    assert wait(lambda: mock.sent, 5), app.mon()
    assert mock.sent[0][2] == "!next D7"
    lg = " ".join(logs(app))
    assert "Fulano desistiu do next de D7" in lg and "20:15 → dentro da janela" in lg, lg


@scenario
def t53_pegaram_next_no_atraso_volta_a_esperar(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", wait=True)
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "pending", 2)
    time.sleep(0.2)
    entra(nxt=["Fulano Rapido"])  # pegaram o next durante o atraso de 1 a 2 s
    assert wait(lambda: app.mon()["waitingFor"] == "Fulano Rapido", 3), app.mon()
    time.sleep(2.5)
    assert not mock.sent and app.mon()["running"] and app.mon()["plan"] == "A", app.mon()
    assert any("Envio cancelado" in x and "Fulano Rapido" in x for x in logs(app)), logs(app)
    entra()  # Fulano desistiu
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next C1"


@scenario
def t54_quem_ja_estava_com_next_desiste_pega(app):
    # 1.4.2: Kira já estava na hunt (com next) ao Iniciar. Com "esperar o next desistir",
    # espera o next dela desistir e então pega.
    connected(app)
    mock.set_desc(desc(OCUP1, *OUTRAS))
    app.start("C1", wait=True)
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3), app.mon()
    time.sleep(1.5)
    assert not mock.sent
    mock.set_desc(desc(OCUP0, *OUTRAS))  # Lord Calvon desistiu
    assert wait(lambda: mock.sent, 5), "não pegou quando o next de quem já estava desistiu"
    assert mock.sent[0][2] == "!next C1"
@scenario
def t55_livre_na_espera_e_entra_outra_pessoa(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", wait=True)
    time.sleep(1)
    entra(nxt=["Lord Calvon"])
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3)
    mock.set_desc(desc(*OUTRAS))  # ficou livre: não é falha
    time.sleep(2)
    m = app.mon()
    assert m["running"] and m["plan"] == "A" and m["waitingFor"] == "Lord Calvon", m
    assert "ficou livre" in m["status"], m["status"]
    entra(who="Gente Nova", t="02:00")  # outra pessoa (não é o next): hunt nova, sem next → pega
    assert wait(lambda: mock.sent, 5), app.mon()


@scenario
def t56_livre_na_espera_e_o_next_entra_falha(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", wait=True)
    time.sleep(1)
    entra(nxt=["Dudu / Pedrinho"])
    assert wait(lambda: app.mon()["waitingFor"] == "Dudu / Pedrinho", 3), app.mon()
    mock.set_desc(desc(*OUTRAS))
    time.sleep(1)
    entra(who="Dudu", t="02:00")  # a dupla do next entrou na hunt: não desistiu
    assert wait(lambda: app.mon()["phase"] == "failed", 3), app.mon()
    assert "não desistiu" in app.mon()["status"], app.mon()["status"]
    time.sleep(1.5)
    assert not mock.sent


@scenario
def t57_espera_em_modo_teste(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", test=True, wait=True)
    time.sleep(1)
    entra(nxt=["Lord Calvon"])
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3)
    entra()
    assert wait(lambda: app.mon()["phase"] == "tested", 5), app.mon()
    time.sleep(1)
    assert not mock.sent and not any(c.startswith("sendtextmessage") for c in mock.commands)


@scenario
def t64_next_some_um_instante_e_entra_na_hunt(app):
    # Na troca, a lista mostra Kira sem next por um instante e logo Lord Calvon caçando:
    # não foi desistência, ele entrou na hunt. Não pode mandar o !next para a hunt dele.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", wait=True)
    time.sleep(1)
    entra(nxt=["Lord Calvon"])
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3)
    entra()  # instante sem o next
    assert wait(lambda: app.mon()["phase"] == "pending", 2), app.mon()
    entra(who="Lord Calvon", t="02:00")
    assert wait(lambda: app.mon()["phase"] == "failed", 4), app.mon()
    assert "não desistiu" in app.mon()["status"], app.mon()["status"]
    time.sleep(2.5)
    assert not mock.sent, "mandou o !next para a hunt de quem estava no next"


# ---------------------------------------------------------------- 1.4.1: formato de 13/10 ("[ 01:45 ]" e "Aceitar")

@scenario
def t65_tempo_com_espacos_e_janela_envia(app):
    # O bug de 13/10: "[ 01:45 ]" com espaços não era lido e, com a janela ligada, nunca enviava.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    st, r = app.start("C1", win=("20:00", "22:30"))
    assert st == 200, r
    time.sleep(1)
    mock.set_desc(desc(L("C1", "01:45", "Lord Tenchi", sp=True), *OUTRAS))
    assert wait(lambda: mock.sent, 5), logs(app)
    assert mock.sent[0][2] == "!next C1"
    lg = " ".join(logs(app))
    assert "Agora 18:30 + falta 01:45 = sua hunt começaria às 20:15 → dentro da janela" in lg, lg
    assert "não consegui ler o tempo" not in lg, lg


@scenario
def t66_tempo_com_espacos_nao_parece_entrada_a_cada_minuto(app):
    # O tempo mudando ([ 02:28 ] → [ 02:27 ]) não pode parecer que alguém entrou de novo.
    # (Com alguém no next, uma "entrada" falsa faria o plano falhar.)
    connected(app)
    mock.set_desc(desc(L("C1", "02:28", "Lord Tenchi", nxt=["Real Calvon"], sp=True), *OUTRAS))
    app.start("C1")
    time.sleep(1.5)
    for t in ("02:27", "02:26", "02:25"):
        mock.set_desc(desc(L("C1", t, "Lord Tenchi", nxt=["Real Calvon"], sp=True), *OUTRAS))
        time.sleep(1.2)
    m = app.mon()
    assert not mock.sent and m["running"] and m["phase"] == "watching", m
    assert not any("entrou" in x for x in logs(app)), logs(app)
    assert m["endsAt"], m  # leu o tempo (mostra a hora que sai)
@scenario
def t67_aceitar_nao_e_entrada_nem_falha(app):
    # Kira caçando com Lord Calvon no next (já estava assim). Kira sai: Lord Calvon "Aceitar".
    # Isso não é entrada nem falha. Quando ele aceita e aparece caçando sem next: pega.
    connected(app)
    mock.set_desc(desc(L("C1", "00:05", "Kira Zinha", nxt=["Lord Calvon"], sp=True), *OUTRAS))
    app.start("C1", "D7")
    time.sleep(1.5)
    mock.set_desc(desc(ACEITAR(), *OUTRAS))
    time.sleep(2.5)
    m = app.mon()
    assert m["running"] and m["plan"] == "A" and not mock.sent, m
    assert "aceitando o respawn" in m["status"], m["status"]
    mock.set_desc(desc(L("C1", "02:00", "Lord Calvon", sp=True), *OUTRAS))
    assert wait(lambda: mock.sent, 5), logs(app)
    assert mock.sent[0][2] == "!next C1"
    assert any("Lord Calvon entrou em C1" in x for x in logs(app)), logs(app)


@scenario
def t68_aceitar_com_espera_desistir(app):
    # Esperando o next desistir: ele vai para "Aceitar" (continua esperando) e depois aceita: falhou.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", wait=True)
    time.sleep(1)
    mock.set_desc(desc(L("C1", "00:10", "Kira Zinha", nxt=["Lord Calvon"], sp=True), *OUTRAS))
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3), app.mon()
    mock.set_desc(desc(ACEITAR(), *OUTRAS))
    time.sleep(2)
    m = app.mon()
    assert m["running"] and m["waitingFor"] == "Lord Calvon" and "aceitando" in m["status"], m
    mock.set_desc(desc(L("C1", "02:00", "Lord Calvon", sp=True), *OUTRAS))
    assert wait(lambda: app.mon()["phase"] == "failed", 3), app.mon()
    assert "não desistiu" in app.mon()["status"], app.mon()["status"]
    time.sleep(2)
    assert not mock.sent


@scenario
def t69_palavra_desconhecida_no_tempo_nao_envia(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    mock.set_desc(desc("C1 - [ Pausa 00:10 ] Livraria de Fire (Tier 3): Fulano", *OUTRAS))
    time.sleep(3)
    assert not mock.sent, "enviou com uma linha que o app não entende"
    avisos = [x for x in logs(app) if "Etiqueta de tempo desconhecida" in x]
    assert len(avisos) == 1, logs(app)
    mock.set_desc(desc(L("C1", "01:00", "Fulano", sp=True), *OUTRAS))  # a linha volta ao normal
    assert wait(lambda: mock.sent, 5), logs(app)


@scenario
def t70_aceitar_durante_o_atraso_cancela(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    mock.set_desc(desc(L("C1", "01:00", "Kira Zinha", sp=True), *OUTRAS))
    assert wait(lambda: app.mon()["phase"] == "pending", 2)
    mock.set_desc(desc(ACEITAR(who="Outro Cara"), *OUTRAS))
    time.sleep(2.5)
    assert not mock.sent and app.mon()["running"], app.mon()
    assert any("Envio cancelado" in x and "aceitando" in x for x in logs(app)), logs(app)
    mock.set_desc(desc(L("C1", "02:00", "Outro Cara", sp=True), *OUTRAS))
    assert wait(lambda: mock.sent, 5)


# ---------------------------------------------------------------- 1.4.2: pega quem JÁ está caçando

@scenario
def t71_ja_cacando_dentro_da_janela_pega(app):
    # O caso de 13/10: 19:26, Maarpz no Y11 com [ 00:35 ] (sai às 20:01), janela 20:00–22:30.
    connected(app)
    y11 = "Y11 - [ 00:35 ] Norcferatu Catacombs Direita (Tier 1): Maarpz"
    mock.set_desc(desc(y11, *OUTRAS))
    st, r = app.start("Y11", win=("20:00", "22:30"), test=True, wait=True)
    assert st == 200, r
    assert wait(lambda: app.mon()["phase"] == "tested", 5), app.mon()
    lg = " ".join(logs(app))
    assert "Maarpz já está caçando em Y11 (Plano A) e ninguém está no next" in lg, lg
    assert "Agora 19:26 + falta 00:35 = sua hunt começaria às 20:01 → dentro da janela" in lg, lg
    assert "teria enviado \"!next Y11\"" in lg, lg
    assert not mock.sent


@scenario
def t72_ja_cacando_fora_da_janela_espera_a_proxima(app):
    # 18:30 + [01:00] = 19:30: fora. Não falha: espera a próxima pessoa (20:15 → pega).
    connected(app)
    mock.set_desc(desc(L(t="01:00"), *OUTRAS))
    app.start("C1", "D7", win=("20:00", "22:30"))
    time.sleep(2.5)
    m = app.mon()
    assert not mock.sent and m["running"] and m["plan"] == "A" and m["phase"] == "watching", m
    assert any("19:30" in x and "fora da janela" in x and "Esse eu não pego" in x for x in logs(app)), logs(app)
    entra(who="Lord Calvon", t="01:45")
    assert wait(lambda: mock.sent, 5), logs(app)
    assert mock.sent[0][2] == "!next C1"


@scenario
def t73_ja_cacando_com_next_espera_a_proxima(app):
    # Sem a opção de esperar: hunt que já existe com next não pega e não falha.
    connected(app)
    mock.set_desc(desc(OCUP1, *OUTRAS))
    app.start("C1", "D7")
    time.sleep(2.5)
    m = app.mon()
    assert not mock.sent and m["running"] and m["plan"] == "A", m
    assert any("Lord Calvon já está no next. Esse eu não pego" in x for x in logs(app)), logs(app)
    entra(who="Lord Calvon", t="02:00")  # Kira saiu, Lord Calvon entrou sem ninguém no next
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next C1"


@scenario
def t74_hunt_do_comeco_next_nao_desiste_nao_falha(app):
    # Com a opção: o next da hunt do começo não desiste e entra na hunt. Não falha:
    # vira uma entrada normal (entrou sem ninguém no next → pega).
    connected(app)
    mock.set_desc(desc(OCUP1, *OUTRAS))
    app.start("C1", "D7", wait=True)
    assert wait(lambda: app.mon()["waitingFor"] == "Lord Calvon", 3), app.mon()
    entra(who="Lord Calvon", t="02:00")
    assert wait(lambda: mock.sent, 5), app.mon()
    assert mock.sent[0][2] == "!next C1" and app.mon()["plan"] == "A"


@scenario
def t75_troca_para_o_B_pega_quem_ja_esta(app):
    connected(app)
    d7 = L("D7", "01:00", "Ja Estava", resp="DT")
    mock.set_desc(desc(d7, L(nxt=["X"]), *OUTRAS))  # C1 já com next: A não pega (espera)
    app.start("C1", "D7")
    time.sleep(1.2)
    assert not mock.sent
    mock.set_desc(desc(d7, L(who="Novo", nxt=["Apressado"]), *OUTRAS))  # alguém entra no C1 já com next: A falha
    assert wait(lambda: mock.sent, 5), app.mon()
    assert mock.sent[0][2] == "!next D7", mock.sent
    assert any("Ja Estava já está caçando em D7 (Plano B)" in x for x in logs(app)), logs(app)


@scenario
def t76_hunt_do_comeco_pegaram_next_no_atraso_nao_falha(app):
    connected(app)
    mock.set_desc(desc(OCUP0, *OUTRAS))
    app.start("C1", "D7")
    assert wait(lambda: app.mon()["phase"] == "pending", 2), app.mon()
    mock.set_desc(desc(OCUP1, *OUTRAS))  # pegaram o next durante o atraso
    time.sleep(2.5)
    m = app.mon()
    assert not mock.sent and m["running"] and m["plan"] == "A", m
    entra(who="Lord Calvon", t="02:00")
    assert wait(lambda: mock.sent, 5)
    assert mock.sent[0][2] == "!next C1"


# ---------------------------------------------------------------- 1.4: gravação

def probe(path):
    """Confere o vídeo com o ffprobe/ffmpeg: formato, tamanho, ritmo, duração e se todos os quadros abrem."""
    r = subprocess.run(["ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
                        "-show_entries", "stream=codec_name,width,height,r_frame_rate,nb_read_frames:format=duration,format_name",
                        "-of", "json", path], capture_output=True, text=True)
    assert r.returncode == 0 and not r.stderr.strip(), r.stderr
    info = json.loads(r.stdout)
    d = subprocess.run(["ffmpeg", "-v", "error", "-i", path, "-f", "null", "-"], capture_output=True, text=True)
    assert d.returncode == 0 and not d.stderr.strip(), "quadros com erro: " + d.stderr[:300]
    st = info["streams"][0]
    assert info["format"]["format_name"] == "avi" and st["codec_name"] == "mjpeg", info
    assert st["r_frame_rate"] == "5/1", st
    # Cada imagem aparece no tempo certo (múltiplos de 0,2 s); as repetidas não ocupam espaço.
    f = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "frame=pts_time", "-of", "csv=p=0", path],
                       capture_output=True, text=True)
    pts = [float(x) for x in f.stdout.split()]
    assert pts and pts[0] == 0 and all(abs(x * 5 - round(x * 5)) < 1e-6 for x in pts), pts[:20]
    assert all(b > a for a, b in zip(pts, pts[1:])), "tempos fora de ordem"
    st["pts"] = pts
    return float(info["format"]["duration"]), st


def frame_png(path, at, out):
    subprocess.run(["ffmpeg", "-v", "error", "-y", "-sseof", str(-at), "-i", path, "-frames:v", "1", out], check=True)


PRINTS = os.environ.get("NEXTTS_PRINTS", "")


@scenario
def t58_gravacao_do_envio(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.set_tswin("Chat privado com x3tBot Kalibra\n<18:00:00> x3tBot Kalibra: Bem-vindo!")
    st, r = app.start("C1", rec=True)
    assert st == 200, r
    s = app.state()
    assert s["monitor"]["record"] and s["rec"]["on"], s["rec"]
    assert any("Gravando a janela do TeamSpeak" in x for x in logs(app)), logs(app)
    time.sleep(1.5)
    entra()
    assert wait(lambda: mock.sent, 5)
    sent_at = time.time()
    hm = time.strftime("%H:%M:%S")
    # O TS mostra a mensagem enviada e a resposta no chat privado.
    app.set_tswin("Chat privado com x3tBot Kalibra\n<18:00:00> x3tBot Kalibra: Bem-vindo!\n"
                  f"<{hm}> \"Real Calvon\": !next C1\n<{hm}> \"x3tBot Kalibra\": Você está no next de C1.")
    assert wait(lambda: app.mon()["phase"] == "sent", 3)
    time.sleep(1)
    s = app.state()
    assert s["rec"]["on"], "parou de gravar logo depois de enviar (devia continuar ~10 s)"
    assert wait(lambda: not app.state()["rec"]["on"], 15), app.state()["rec"]
    tail = time.time() - sent_at
    assert 9.5 <= tail <= 13.5, f"continuou gravando {tail:.1f}s depois de enviar"
    assert wait(lambda: any("Gravação salva" in x for x in logs(app)), 3), logs(app)
    vids = app.videos()
    assert len(vids) == 1 and re.search(r"\d{4}-\d\d-\d\d_\d\d-\d\d-\d\d_C1\.avi$", vids[0]), vids
    dur, st = probe(vids[0])
    assert 12 <= dur <= 17, f"duração {dur}"
    assert st["width"] == 1000 and st["height"] == 640, st  # só a janela do TS (sem faixa nem bordas)
    assert os.path.getsize(vids[0]) < 3_000_000, os.path.getsize(vids[0])  # quadros iguais não ocupam espaço
    assert app.state()["rec"]["last"] == os.path.basename(vids[0])
    if PRINTS:
        frame_png(vids[0], 3, os.path.join(PRINTS, "video-depois-do-envio.png"))


@scenario
def t59_gravacao_ts_minimizado_e_volta(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.set_tswin("min")
    app.start("C1", rec=True)
    assert wait(lambda: any("minimizado" in x for x in logs(app)), 3), logs(app)
    assert "minimizado" in app.state()["rec"]["note"], app.state()["rec"]
    if PRINTS:
        time.sleep(0.6)
    app.set_tswin("Chat privado com x3tBot Kalibra")
    assert wait(lambda: any("voltei a ver a janela" in x for x in logs(app)), 3), logs(app)
    assert app.state()["rec"]["note"] == ""
    time.sleep(0.5)
    app.api("stop", {})
    assert wait(lambda: not app.state()["rec"]["on"], 5), "não terminou a gravação depois do Parar"
    vids = app.videos()
    dur, st = probe(vids[0])
    assert dur >= 2, dur
    assert st["width"] == 1280, st  # começou sem janela: tamanho padrão
    if PRINTS:
        frame_png(vids[0], dur - 0.3, os.path.join(PRINTS, "video-ts-minimizado.png"))


@scenario
def t60_sem_gravacao_nao_cria_video(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1")
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "sent", 5)
    time.sleep(1)
    assert not app.videos() and not app.state()["rec"]["on"] and not app.mon()["record"]


@scenario
def t61_fechar_app_gravando_salva_o_video(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", rec=True)
    time.sleep(2.2)
    st, _ = app.api("quit", {})
    assert st == 200
    assert wait(lambda: app.p.poll() is not None, 8), "app não fechou"
    vids = app.videos()
    assert len(vids) == 1
    dur, _ = probe(vids[0])
    assert dur >= 2, dur
    with open(vids[0], "rb") as f:
        assert b"idx1" in f.read(), "vídeo sem índice (não terminou de salvar)"


@scenario
def t62_opcoes_lembradas_e_teste_grava(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", test=True, wait=True, rec=True)
    time.sleep(1)
    entra()
    assert wait(lambda: app.mon()["phase"] == "tested", 5)
    assert app.state()["rec"]["on"], "modo teste também deve gravar até 10 s depois"
    assert wait(lambda: not app.state()["rec"]["on"], 14)
    vids = app.videos()
    assert len(vids) == 1 and vids[0].endswith("_C1_teste.avi"), vids
    probe(vids[0])
    app.close()
    app2 = App(app.data)
    try:
        assert wait(lambda: app2.state()["connected"], 6)
        s = app2.state()
        assert s["waitGiveUp"] is True and s["record"] is True, s
    finally:
        app2.close()


@scenario
def t63_nova_gravacao_nao_apaga_a_anterior(app):
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.start("C1", rec=True)
    time.sleep(1.2)
    app.api("stop", {})
    app.start("C1", rec=True)  # começa outra logo em seguida (a anterior ainda terminando)
    time.sleep(1.2)
    app.api("stop", {})
    assert wait(lambda: not app.state()["rec"]["on"], 6)
    time.sleep(0.5)
    vids = app.videos()
    assert len(vids) == 2, vids
    for v in vids:
        probe(v)


@scenario
def t77_aviso_rolando_nao_enche_o_video(app):
    # O TS de verdade tem um aviso que passa rolando na barra de baixo. Ele não pode fazer
    # gravar imagem nova a cada 0,2 s; uma mensagem nova no chat tem que entrar na hora.
    connected(app)
    mock.set_desc(desc(*OUTRAS))
    app.set_tswin("Chat privado com x3tBot Kalibra\nROLANDO: Announcement - Start your own Community Server")
    t0 = time.time()
    app.start("C1", rec=True)
    time.sleep(6)
    chat_at = time.time() - t0
    app.set_tswin("Chat privado com x3tBot Kalibra\n<20:15:03> \"Real Calvon\": !next C1\n"
                  "ROLANDO: Announcement - Start your own Community Server")
    time.sleep(2)
    app.api("stop", {})
    assert wait(lambda: not app.state()["rec"]["on"], 6)
    dur, st = probe(app.videos()[0])
    pts = st["pts"]
    print(f"    imagens gravadas: {len(pts)} em {dur:.1f} s; mensagem do chat aos {chat_at:.1f} s; tempos: {pts}")
    assert len(pts) <= dur * 5 * 0.4, f"gravou imagem nova demais por causa do aviso rolando: {len(pts)} em {dur} s"
    assert any(chat_at - 0.3 <= p <= chat_at + 0.8 for p in pts), f"a mensagem do chat não entrou na hora ({chat_at:.1f} s): {pts}"
    gaps = [b - a for a, b in zip(pts, pts[1:])]
    assert max(gaps) <= 2.3, f"o aviso rolando ficou parado mais de 2 s no vídeo: {gaps}"


print()
ok = sum(1 for r in RESULTS if r[1])
print(f"{ok}/{len(RESULTS)} cenários aprovados")
sys.exit(0 if ok == len(RESULTS) else 1)
