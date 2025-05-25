from flask import Flask
import os

app = Flask(__name__)

@app.route('/')
def hello():
    pod_name = os.environ.get("POD_NAME", "unknown-pod")
    return f"Hi! This is a test service running in pod: {pod_name}"

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8080)
