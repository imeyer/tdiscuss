// Theme management
function getTheme() {
    return localStorage.getItem('theme') || 'auto';
}

function setTheme(theme) {
    localStorage.setItem('theme', theme);
    applyTheme(theme);
}

function applyTheme(theme) {
    if (theme === 'auto') {
        // Remove data-theme attribute to use system preference
        document.documentElement.removeAttribute('data-theme');
    } else {
        // Apply specific theme
        document.documentElement.setAttribute('data-theme', theme);
    }
    
    // Update active button
    updateActiveButton(theme);
}

function updateActiveButton(theme) {
    // Remove active class from all buttons
    document.querySelectorAll('.theme-btn').forEach(btn => {
        btn.classList.remove('active');
    });
    
    // Add active class to current theme button
    const activeBtn = document.querySelector(`.theme-btn[data-theme="${theme}"]`);
    if (activeBtn) {
        activeBtn.classList.add('active');
    }
}

// Apply saved theme on page load
document.addEventListener('DOMContentLoaded', function() {
    const savedTheme = getTheme();
    applyTheme(savedTheme);
    
    // Add event listeners to theme buttons
    document.querySelectorAll('.theme-btn').forEach(btn => {
        btn.addEventListener('click', function() {
            const theme = this.getAttribute('data-theme');
            setTheme(theme);
        });
    });
});

// Apply theme immediately to prevent flash
(function() {
    const savedTheme = localStorage.getItem('theme');
    if (savedTheme && savedTheme !== 'auto') {
        document.documentElement.setAttribute('data-theme', savedTheme);
    }
})();