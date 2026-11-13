"""Testa a janela (interface) num Chromium de verdade, com o TS simulado."""
import os
import re
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from mock_ts3 import MockTS3  # noqa: E402
from cenarios_util import desc, OCUP0, OCUP1, OUTRAS  # noqa: E402
from playwright.sync_api import sync_playwright  # noqa: E402

BIN, OUT = sys.argv[1], sys.argv[2]
os.makedirs(OUT, exist_ok=True)
mock = MockTS3(25639, "ABCD-1234-EFGH")
mock.set_desc(desc(*OUTRAS))
data = tempfile.mkdtemp()
tswin = os.path.join(data, "janela_ts.txt")
open(tswin, "w").write("Chat privado com x3tBot Kalibra")
p = subprocess.Popen([BIN], env=dict(os.environ, NEXTTS_DATA=data, NEXTTS_NO_BROWSER="1", NEXTTS_TEST_CLOCK="18:30:00", NEXTTS_FAKE_TS=tswin), stdout=subprocess.PIPE, text=True)
url = re.search(r"(http://\S+)", p.stdout.readline()).group(1)
errors = []
try:
    with sync_playwright() as pw:
        b = pw.chromium.launch()
        pg = b.new_page(viewport={"width": 504, "height": 860}, bypass_csp=True)
        pg.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
        pg.on("pageerror", lambda e: errors.append(str(e)))
        pg.goto(url)
        pg.wait_for_selector("#pill")
        time.sleep(0.8)
        pg.screenshot(path=f"{OUT}/1-desconectado.png", full_page=True)
        assert pg.is_disabled("#go")

        pg.click("#connect")  # sem chave
        time.sleep(0.4)
        assert "Cole a API Key" in pg.inner_text("#notice") and pg.is_visible("#notice"), pg.inner_text("#notice")
        pg.fill("#key", "errada")
        pg.click("#connect")
        pg.wait_for_function("document.getElementById('connErr').innerText.includes('recusou')")
        pg.screenshot(path=f"{OUT}/2-chave-errada.png", full_page=True)

        pg.fill("#key", "ABCD-1234-EFGH")
        pg.press("#key", "Enter")
        pg.wait_for_selector("#connDone:not(.hidden)")
        time.sleep(0.8)
        assert pg.input_value("#channel") == "12", pg.input_value("#channel")
        assert pg.input_value("#bot") == "BOTUID+abc/="
        assert "!next C1" in pg.inner_text("#preview")
        assert pg.inner_text("#speech") == "Escolha um respawn!", pg.inner_text("#speech")
        # Atalhos de claim: x3tBot (selecionado) e MissClick Bot Test
        pg.wait_for_function("document.querySelectorAll('#chips button').length === 2")
        chips = pg.query_selector_all("#chips button")
        assert "x3tBot" in chips[0].inner_text() and "sel" in chips[0].get_attribute("class"), chips[0].inner_text()
        assert "MissClick" in chips[1].inner_text()
        pg.screenshot(path=f"{OUT}/3-conectado.png", full_page=True)
        chips[1].click()
        time.sleep(0.3)
        assert pg.input_value("#channel") == "15" and pg.input_value("#bot") == "MCBOT/xyz=", (pg.input_value("#channel"), pg.input_value("#bot"))
        pg.wait_for_function("document.getElementById('chanInfo').innerText.includes('sem a lista')")
        assert "sel" in pg.query_selector_all("#chips button")[1].get_attribute("class")
        pg.query_selector_all("#chips button")[0].click()
        time.sleep(0.3)
        assert pg.input_value("#channel") == "12" and pg.input_value("#bot") == "BOTUID+abc/="

        # Lista de respawns completa, agrupada por cidade
        n = pg.evaluate("document.querySelectorAll('#codeSel option').length")
        assert n >= 117, n
        assert pg.input_value("#codeSel") == "C1"
        pg.wait_for_function("document.getElementById('chanInfo').innerText.includes('Lista encontrada')", timeout=4000)
        assert "livre" in pg.inner_text("#live"), pg.inner_text("#live")
        pg.select_option("#codeSel", "A1")
        pg.wait_for_function("document.getElementById('live').innerText.includes('Jairo')")
        # Canal de teste vazio avisa
        pg.select_option("#channel", "5")
        pg.wait_for_function("document.getElementById('chanInfo').innerText.includes('sem a lista')")
        # Iniciar num canal sem lista: o aviso aparece e continua na tela (não some na próxima atualização).
        pg.click("#go")
        pg.wait_for_selector("#notice:not(.hidden)")
        time.sleep(1.5)
        assert "sem a lista" in pg.inner_text("#notice") and pg.is_visible("#notice"), pg.inner_text("#notice")
        pg.screenshot(path=f"{OUT}/3b-canal-errado.png", full_page=True)
        pg.click("#notice")
        assert not pg.is_visible("#notice")
        pg.select_option("#channel", "12")
        # Outro código (digitar)
        pg.select_option("#codeSel", "__other")
        pg.fill("#code", "xx")
        time.sleep(0.2)
        assert pg.is_disabled("#go") and "inválido" in pg.inner_text("#codeName")
        pg.fill("#code", "c1")
        time.sleep(0.2)
        assert not pg.is_disabled("#go") and "Livraria de Fire" in pg.inner_text("#codeName")
        pg.select_option("#codeSel", "C1")
        # Plano B igual ao A: não deixa iniciar
        pg.select_option("#codeSelB", "C1")
        time.sleep(0.2)
        assert pg.is_disabled("#go") and "mesmo respawn" in pg.inner_text("#codeNameB"), pg.inner_text("#codeNameB")
        pg.select_option("#codeSelB", "A1")
        pg.wait_for_function("document.getElementById('liveB').innerText.includes('Jairo')")
        assert "!next A1" in pg.inner_text("#preview"), pg.inner_text("#preview")
        # Janela de horário
        assert pg.is_disabled("#winStart")
        pg.check("#windowOn")
        assert not pg.is_disabled("#winStart")
        pg.fill("#winStart", "25:00")
        time.sleep(0.2)
        assert pg.is_disabled("#go") and "Preencha" in pg.inner_text("#winHint"), pg.inner_text("#winHint")
        pg.fill("#winStart", "2000"); pg.press("#winStart", "Tab")
        assert pg.input_value("#winStart") == "20:00", pg.input_value("#winStart")
        pg.fill("#winEnd", "22h30"); pg.press("#winEnd", "Tab")
        assert pg.input_value("#winEnd") == "22:30", pg.input_value("#winEnd")
        time.sleep(0.3)
        assert "entre 20:00 e 22:30" in pg.inner_text("#preview"), pg.inner_text("#preview")
        assert pg.inner_text("#clock").startswith("18:3"), pg.inner_text("#clock")
        time.sleep(1.5)
        pg.screenshot(path=f"{OUT}/3c-lista.png", full_page=True)

        pg.click("#go")
        pg.wait_for_function("document.getElementById('go').textContent.includes('Parar')")
        time.sleep(1.2)
        assert "livre" in pg.inner_text("#cardStatus").lower(), pg.inner_text("#cardStatus")
        assert "Plano A · C1" in pg.inner_text("#planline") and "Plano B · A1" in pg.inner_text("#planline")
        assert "janela 20:00 – 22:30" in pg.inner_text("#summary"), pg.inner_text("#summary")
        assert pg.is_disabled("#code")
        assert pg.inner_text("#speech") == "De olho no C1...", pg.inner_text("#speech")
        assert pg.get_attribute("#hero", "class") == "sob"
        pg.screenshot(path=f"{OUT}/4-vigiando-livre.png", full_page=True)

        mock.set_desc(desc(OCUP0, *OUTRAS))
        pg.wait_for_selector("#count:not(.hidden)", timeout=3000)
        pg.screenshot(path=f"{OUT}/5-enviando-em.png", full_page=True)
        pg.wait_for_function("document.getElementById('go').textContent.includes('Iniciar')", timeout=5000)
        time.sleep(0.8)
        assert mock.sent and mock.sent[0][2] == "!next C1"
        assert "Enviado" in pg.inner_text("#stText")
        assert "respondeu" in pg.inner_text("#log")
        assert "18:30 + falta 01:30 = sua hunt começaria às 20:00 → dentro da janela" in pg.inner_text("#calc"), pg.inner_text("#calc")
        assert pg.inner_text("#speech") == "Peguei o next!" and pg.get_attribute("#hero", "class") == "cheer"
        # A Situação (com o resultado) fica à vista depois que termina.
        time.sleep(0.8)
        box = pg.evaluate("(() => { const r = document.getElementById('cardStatus').getBoundingClientRect(); return [r.top, r.bottom, innerHeight]; })()")
        assert box[0] >= 0 and box[1] <= box[2], f"Situação fora da tela depois do envio: {box}"
        pg.screenshot(path=f"{OUT}/6-enviado.png", full_page=True)

        # Recarregar a página não pode fechar o app nem perder o estado
        pg.reload()
        time.sleep(1)
        assert "Enviado" in pg.inner_text("#stText")
        pg.set_viewport_size({"width": 380, "height": 800})
        time.sleep(0.3)
        sw = pg.evaluate("document.documentElement.scrollWidth")
        assert sw <= 380, f"rolagem horizontal em janela estreita: {sw}"
        pg.screenshot(path=f"{OUT}/7-estreito.png", full_page=True)

        # 1.4: esperar o next desistir + gravação
        pg.set_viewport_size({"width": 504, "height": 860})
        assert not pg.is_checked("#waitGiveUp") and not pg.is_checked("#record")
        assert not pg.is_visible("#recHint")
        pg.check("#waitGiveUp")
        pg.check("#record")
        time.sleep(0.2)
        assert pg.is_visible("#recHint") and "chat privado com o bot" in pg.inner_text("#recHint"), pg.inner_text("#recHint")
        pv = pg.inner_text("#preview")
        assert "quando ele desistir" in pv and "Grava vídeo" in pv, pv
        pg.click("#openRec")  # abre a pasta (nos testes não abre nada, mas não pode dar erro)
        time.sleep(0.4)
        assert not pg.is_visible("#notice"), pg.inner_text("#notice")
        pg.locator("#waitGiveUp").scroll_into_view_if_needed()
        pg.screenshot(path=f"{OUT}/8-opcoes-novas.png", full_page=True)
        mock.set_desc(desc(*OUTRAS))
        pg.click("#go")
        pg.wait_for_function("document.getElementById('go').textContent.includes('Parar')")
        assert pg.is_disabled("#waitGiveUp") or not pg.is_visible("#waitGiveUp")
        pg.wait_for_function("document.getElementById('recLine').innerText.includes('Gravando a 30 fps')", timeout=3000)
        assert "últimos 20 s" in pg.inner_text("#recLine"), pg.inner_text("#recLine")
        time.sleep(1.2)
        pg.locator("#cardStatus").screenshot(path=f"{OUT}/9b-gravando-ultimos-20s.png")
        assert "espera o next desistir" in pg.inner_text("#summary"), pg.inner_text("#summary")
        time.sleep(1)
        mock.set_desc(desc(OCUP1, *OUTRAS))  # entrou com alguém no next: espera ele desistir
        pg.wait_for_function("document.getElementById('stText').innerText.includes('Esperando o next desistir')", timeout=3000)
        assert pg.inner_text("#speech") == "Desiste do next, vai...", pg.inner_text("#speech")
        time.sleep(1.5)
        assert len(mock.sent) == 1, mock.sent
        pg.screenshot(path=f"{OUT}/9-esperando-desistir.png", full_page=True)
        mock.set_desc(desc(OCUP0, *OUTRAS))  # o next desistiu
        pg.wait_for_function("document.getElementById('go').textContent.includes('Iniciar')", timeout=5000)
        assert len(mock.sent) == 2 and mock.sent[1][2] == "!next C1", mock.sent
        time.sleep(0.8)
        rl = pg.inner_text("#recLine")
        assert "10 s depois do envio" in rl and "faltam" in rl, rl
        pg.locator("#cardStatus").screenshot(path=f"{OUT}/10b-gravando-10s-depois.png")
        assert "desistiu do next" in pg.inner_text("#log")
        pg.screenshot(path=f"{OUT}/10-enviado-gravando.png", full_page=True)
        pg.wait_for_function("document.getElementById('recLine').innerText.includes('Vídeo salvo')", timeout=15000)
        assert "depois do envio" in pg.inner_text("#recLine"), pg.inner_text("#recLine")
        pg.locator("#cardStatus").screenshot(path=f"{OUT}/11b-video-salvo.png")
        pg.screenshot(path=f"{OUT}/11-video-salvo.png", full_page=True)
        pg.click("#recLine button")
        time.sleep(0.4)
        assert not pg.is_visible("#notice")
        assert "Confirmado pelo bot" in pg.inner_text("#reply"), pg.inner_text("#reply")  # resposta "posição 1"
        assert not pg.is_disabled("#openVideo")
        pg.click("#openVideo")
        time.sleep(0.4)
        assert not pg.is_visible("#notice"), pg.inner_text("#notice")

        # 1.5.0: Plano C e planos salvos
        assert pg.is_visible("#planC")  # o Plano B (A1) está escolhido
        pg.select_option("#codeSelC", "A1")
        time.sleep(0.2)
        assert pg.is_disabled("#go") and "não pode repetir" in pg.inner_text("#codeNameC"), pg.inner_text("#codeNameC")
        pg.select_option("#codeSelC", "Y11")
        time.sleep(0.3)
        assert "!next Y11" in pg.inner_text("#preview") and "Plano C" in pg.inner_text("#preview"), pg.inner_text("#preview")
        pg.once("dialog", lambda d: d.accept("Noite"))
        pg.click("#saveFav")
        pg.wait_for_function("document.querySelectorAll('#favChips button').length === 1")
        # Muda tudo e carrega o plano salvo de volta.
        pg.select_option("#codeSelB", "")
        time.sleep(0.2)
        assert not pg.is_visible("#planC")
        pg.select_option("#codeSel", "D7")
        pg.uncheck("#windowOn")
        pg.query_selector_all("#favChips button")[0].click()
        time.sleep(0.4)
        assert pg.input_value("#codeSel") == "C1" and pg.input_value("#codeSelB") == "A1" and pg.input_value("#codeSelC") == "Y11", \
            (pg.input_value("#codeSel"), pg.input_value("#codeSelB"), pg.input_value("#codeSelC"))
        assert pg.is_checked("#windowOn") and pg.is_checked("#waitGiveUp") and pg.is_checked("#record")
        pg.locator("#favBox").scroll_into_view_if_needed()
        pg.screenshot(path=f"{OUT}/12-planos-salvos-e-plano-c.png", full_page=True)

        # 1.7.0: tempo personalizado do claim
        def shot_dur(name):  # a opção, os campos e o comando (para a prévia)
            pg.evaluate("document.getElementById('durOn').closest('label').scrollIntoView({block: 'center'})")
            time.sleep(0.1)
            a = pg.locator("#durOn").locator("xpath=ancestor::label").bounding_box()
            z = pg.locator("#durHint").bounding_box()
            pg.screenshot(path=f"{OUT}/{name}", clip={"x": 0, "y": a["y"] - 6, "width": 504, "height": z["y"] + z["height"] - a["y"] + 12})
        pg.uncheck("#windowOn"); pg.uncheck("#record"); pg.uncheck("#waitGiveUp")
        pg.locator("#durOn").scroll_into_view_if_needed()
        assert not pg.is_checked("#durOn") and pg.is_disabled("#durH") and pg.is_disabled("#durM")
        assert "sem tempo" in pg.inner_text("#durHint"), pg.inner_text("#durHint")
        shot_dur("15a-tempo-desligado.png")
        pg.locator("#preview").screenshot(path=f"{OUT}/15b-previa-sem-tempo.png")
        assert "!next C1 (ou !next A1 no Plano B, !next Y11 no Plano C)" in pg.inner_text("#preview"), pg.inner_text("#preview")
        pg.check("#durOn")
        time.sleep(0.2)
        assert not pg.is_disabled("#durH")
        pg.fill("#durH", "2"); pg.fill("#durM", "10"); pg.press("#durM", "Tab")
        time.sleep(0.3)
        hint = pg.inner_text("#durHint")
        assert "!next C1 2:10" in hint and "2 horas e 10 minutos" in hint, hint
        pv = pg.inner_text("#preview")
        assert "!next C1 2:10 (ou !next A1 2:10 no Plano B, !next Y11 2:10 no Plano C)" in pv, pv
        shot_dur("15c-tempo-2h10.png")
        pg.locator("#preview").screenshot(path=f"{OUT}/15d-previa-com-tempo.png")
        pg.select_option("#codeSel", "D7")  # troca o respawn: a prévia acompanha
        time.sleep(0.2)
        assert "!next D7 2:10" in pg.inner_text("#preview") and "!next D7 2:10" in pg.inner_text("#durHint"), pg.inner_text("#preview")
        pg.select_option("#codeSel", "C1")
        pg.fill("#durM", "5"); pg.press("#durM", "Tab")  # minutos sempre com 2 algarismos
        time.sleep(0.2)
        assert pg.input_value("#durM") == "05" and "!next C1 2:05" in pg.inner_text("#preview"), pg.input_value("#durM")
        for h, m, why in [("2", "75", "0 a 59"), ("0", "0", "zero"), ("0", "00", "zero"), ("-1", "10", "negativa"), ("", "10", "Preencha"), ("2", "x", "só números")]:
            pg.fill("#durH", h); pg.fill("#durM", m)
            time.sleep(0.15)
            assert why in pg.inner_text("#durHint") and pg.is_disabled("#go"), (h, m, pg.inner_text("#durHint"))
            if (h, m) == ("2", "75"):
                shot_dur("15e-tempo-invalido.png")
            assert "Arrume o tempo personalizado" in pg.inner_text("#preview"), pg.inner_text("#preview")
        pg.fill("#durH", "2"); pg.fill("#durM", "30"); pg.press("#durM", "Tab")
        time.sleep(0.4)
        assert not pg.is_disabled("#go")
        pg.locator("#durOn").scroll_into_view_if_needed()
        pg.screenshot(path=f"{OUT}/13-tempo-personalizado.png", full_page=True)
        pg.locator("#durOn").locator("xpath=ancestor::label").screenshot(path=f"{OUT}/13b-opcao-tempo.png")
        pg.reload()  # lembrado ao abrir de novo
        pg.wait_for_selector("#pill")
        pg.wait_for_function("document.getElementById('pill').textContent.includes('Conectado')", timeout=5000)
        time.sleep(0.8)
        assert pg.is_checked("#durOn") and pg.input_value("#durH") == "2" and pg.input_value("#durM") == "30", \
            (pg.is_checked("#durOn"), pg.input_value("#durH"), pg.input_value("#durM"))
        # Iniciar (modo teste): o tempo trava e o comando completo aparece no Histórico.
        mock.set_desc(desc(*OUTRAS))
        pg.check("#testMode")
        time.sleep(0.3)
        pg.click("#go")
        pg.wait_for_function("document.getElementById('go').textContent.includes('Parar')")
        time.sleep(0.4)
        assert pg.is_disabled("#durOn") and pg.is_disabled("#durH") and pg.is_disabled("#durM")
        assert "tempo 2:30" in pg.inner_text("#summary"), pg.inner_text("#summary")
        pg.locator("#cardStatus").screenshot(path=f"{OUT}/14-monitorando-com-tempo.png")
        n = len(mock.sent)
        mock.set_desc(desc(OCUP0, *OUTRAS))
        pg.wait_for_function("document.getElementById('log').innerText.includes('teria enviado')", timeout=6000)
        assert 'teria enviado "!next C1 2:30"' in pg.inner_text("#log"), pg.inner_text("#log")
        assert len(mock.sent) == n
        pg.uncheck("#testMode")
        pg.uncheck("#durOn")  # desligado: volta ao formato de sempre
        time.sleep(0.3)
        assert "!next C1 (ou" in pg.inner_text("#preview"), pg.inner_text("#preview")
        pg.reload()
        pg.wait_for_selector("#pill")
        time.sleep(1.2)
        assert not pg.is_checked("#durOn") and pg.input_value("#durH") == "2" and pg.input_value("#durM") == "30"
        b.close()
    errors = [e for e in errors if "status of 400" not in e]  # 400 = chave errada, esperado
    assert not errors, errors
    print("INTERFACE OK")
finally:
    p.terminate()
