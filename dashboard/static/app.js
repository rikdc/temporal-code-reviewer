// Connect to SSE endpoint
const eventSource = new EventSource(`/api/events?workflowId=${workflowId}`);

eventSource.onmessage = (event) => {
    const data = JSON.parse(event.data);
    console.log('Event received:', data);
    updateAgentUI(data.agent_name, data.progress, data.event_type, data.result);
};

eventSource.onerror = (error) => {
    console.error('SSE connection error:', error);
    // Auto-reconnect is handled by EventSource
};

function updateAgentUI(agentName, progress, eventType, result) {
    const lane = document.getElementById(`agent-${agentName}`);
    if (!lane) {
        console.warn(`Agent lane not found: ${agentName}`);
        return;
    }

    const progressBar = lane.querySelector('.progress-fill');
    const progressText = lane.querySelector('.progress-text');
    const status = lane.querySelector('.status');
    const findingsDiv = lane.querySelector('.findings');

    if (eventType === 'agent_started') {
        lane.classList.add('running');
        lane.classList.remove('pending', 'completed', 'failed');
        status.textContent = 'Running';
        progressBar.style.width = '0%';
        progressText.textContent = '0%';
    } else if (eventType === 'agent_progress') {
        progressBar.style.width = `${progress}%`;
        progressText.textContent = `${progress}%`;
    } else if (eventType === 'agent_completed') {
        lane.classList.add('completed');
        lane.classList.remove('running', 'pending', 'failed');
        status.textContent = 'Complete';
        progressBar.style.width = '100%';
        progressText.textContent = '100%';

        // Display findings if available (XSS-safe)
        if (result && result.findings) {
            findingsDiv.textContent = '';
            const ul = document.createElement('ul');
            result.findings.forEach(f => {
                const li = document.createElement('li');
                li.textContent = f;
                ul.appendChild(li);
            });
            findingsDiv.appendChild(ul);
        }
    } else if (eventType === 'agent_failed') {
        lane.classList.add('failed');
        lane.classList.remove('running', 'pending', 'completed');
        status.textContent = 'Failed';

        if (result && result.error) {
            const p = document.createElement('p');
            p.className = 'error';
            p.textContent = result.error;
            findingsDiv.appendChild(p);
        }
    }
}

// Add visual indicator for connection status
window.addEventListener('load', () => {
    console.log(`Dashboard loaded for workflow: ${workflowId}`);
});
