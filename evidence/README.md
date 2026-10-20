# Evidências de Validação — Web-Server Cold Chain

Arquivos de captura exigidos para a entrega da disciplina (Seção 14):
1. `01-login.png`: Banner /etc/issue e login do usuário coldadmin.
2. `02-ip-raspberry.png`: `ip addr` com IP e a bridge br-coldchain.
3. `03-docker-running.png`: `docker version` e `docker info`.
4. `04-container-running.png`: `docker ps` e `docker inspect` dos dois containers.
5. `05-curl-local.png`: `curl -s http://127.0.0.1:5000` exibindo a frase exata.
6. `06-acesso-notebook.png`: Navegador acessando o painel web com o <h1>.
7. `07-boot-time.png`: Tabela do `measure-boot.sh` comprovando SLA <= 15s.
8. `08-mqtt-seguranca.png`: Conexão anônima recusada e ACL negando tópicos alheios.
9. `09-docker-images.png`: `docker images` com imagens locais offline.
