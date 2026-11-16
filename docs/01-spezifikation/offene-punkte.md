# Offene Punkte

- **Planner und Worker:** Ob die Ticket-Planung in den Worker wandert, ist zurückgestellt ([Offen in der Planung](planung.md#offen-in-der-planung)).
- **Erprobung:** Das Plugin ist in keiner echten Sitzung mit Board gelaufen, und die Fassung 0.3.0 ist noch nicht mit `claude plugin validate` geprüft. Ungeprüft sind der Start mit `--agent` und Skill-Aufruf als erstem Auftrag, das Lesen und Schreiben im Ticketsystem aus den Skills und ob `init` seine Vorlagen findet.
- **Agent als Systemprompt:** Mit `--agent` ersetzt der kurze Agent-Text den Standard-Systemprompt von Claude Code. Alternative: ohne `--agent` starten und nur den Skill aufrufen, das Modell über `--model` setzen. Dann wären die Agents entbehrlich.
- **R0 und R1:** Beide werden gleich behandelt. Entweder zusammenlegen oder R0 unterscheiden, etwa ohne Review.
- **Typ:** Das Feld steuert noch keinen Ablauf. Concept-, Bug- und Organisations-Tickets laufen wie Implementation oder gar nicht.
