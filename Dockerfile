FROM ghcr.io/nodis-com-br/python:3.10.4

WORKDIR /app
COPY Pipfile /app/
COPY Pipfile.lock /app/

RUN pipenv install --deploy --system

COPY src /app
CMD ["uwsgi", "--module", "app:app"]
