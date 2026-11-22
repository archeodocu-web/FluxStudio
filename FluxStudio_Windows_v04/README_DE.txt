FLUX STUDIO – WINDOWS-KORREKTUR 0.4
===================================

Korrektur zum Startfehler aus dem Windows-Fehlerdialog
------------------------------------------------------
Die vorherige Version hat GetStockObject fälschlich aus user32.dll geladen.
GetStockObject gehört zur Windows-GDI-Bibliothek gdi32.dll. Dadurch brach
Flux Studio beim Erzeugen der Steuerelemente bereits beim Start ab.
Version 0.4 bindet GetStockObject nun korrekt aus gdi32.dll ein.

Das AI-Icon ist als Win32-Ressource (RT_GROUP_ICON und RT_ICON) in der EXE
eingebettet. Die EXE und der Quellcode liegen in diesem Paket.

Verwendung
----------
1. FluxStudio.exe per Doppelklick starten.
2. Beim ersten Start „Einrichtung starten“ wählen.
3. Die Vulkan-Engine und die Modellkomponenten werden heruntergeladen.
4. Danach Motiv, Umgebung und Stil eingeben und „Bild generieren / bearbeiten“
   verwenden.
5. Für die Bearbeitung eines bestehenden Bildes vorher „Bild auswählen“ wählen.

Protokoll
---------
%LOCALAPPDATA%\FluxStudio\logs\FluxStudio.log

Technische Hinweise
-------------------
- Windows x64 GUI-Anwendung.
- C++-Bildengine: stable-diffusion.cpp mit Vulkan.
- Bildmodell: FLUX.2 Klein 4B GGUF Q4_0.
- Textencoder: Qwen3 4B GGUF Q4_K_S.
- VAE: FLUX.2 VAE.
- Keine Python-Laufzeit und kein Cloud-Konto für die lokale Bildgenerierung.
- Internet ist nur für den erstmaligen Download von Engine und Modellen nötig.

Wichtige Einschränkung
---------------------
Die Anwendung wurde in dieser Umgebung als Windows-x64-Programm kompiliert.
Der konkrete Start konnte nicht auf dem Rechner des Anwenders mit Windows 10
und der AMD-Grafikkarte vor Ort ausgeführt werden. Der konkrete Fehler aus dem
Screenshot (fehlender GetStockObject-Export in user32.dll) ist im Quellcode
korrigiert. Die erfolgreiche GPU-Bildgenerierung auf dem Ziel-PC ist damit
noch nicht nachgewiesen.

Projektquellen
--------------
Engine: https://github.com/leejet/stable-diffusion.cpp
FLUX.2 Klein: https://huggingface.co/manishdashsharma/flux2-klein-4b-gguf
Win32 GetStockObject Dokumentation:
https://learn.microsoft.com/de-de/windows/win32/api/wingdi/nf-wingdi-getstockobject
