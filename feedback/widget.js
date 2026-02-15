(function(){
  'use strict';
  var script = document.currentScript;
  var base = (script && script.getAttribute('data-base')) || '/feedback';
  var tokenKey = script && script.getAttribute('data-token-key');

  // Load CSS
  var link = document.createElement('link');
  link.rel = 'stylesheet';
  link.href = base + '/widget.css';
  document.head.appendChild(link);

  // Floating button
  var btn = document.createElement('button');
  btn.className = 'hfb-btn';
  btn.setAttribute('aria-label', 'Envoyer un commentaire');
  btn.innerHTML = '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>';
  document.body.appendChild(btn);

  // Overlay
  var overlay = document.createElement('div');
  overlay.className = 'hfb-overlay hfb-hidden';
  overlay.innerHTML =
    '<div class="hfb-panel">' +
      '<div class="hfb-header">' +
        '<span class="hfb-title">Envoyer un commentaire</span>' +
        '<button class="hfb-close" aria-label="Fermer">&times;</button>' +
      '</div>' +
      '<textarea class="hfb-textarea" placeholder="Votre remarque, suggestion ou bug..." rows="5"></textarea>' +
      '<div class="hfb-footer">' +
        '<span class="hfb-status"></span>' +
        '<button class="hfb-submit">Envoyer</button>' +
      '</div>' +
    '</div>';
  document.body.appendChild(overlay);

  var panel = overlay.querySelector('.hfb-panel');
  var textarea = overlay.querySelector('.hfb-textarea');
  var submitBtn = overlay.querySelector('.hfb-submit');
  var closeBtn = overlay.querySelector('.hfb-close');
  var status = overlay.querySelector('.hfb-status');

  function open() {
    overlay.classList.remove('hfb-hidden');
    textarea.value = '';
    status.textContent = '';
    submitBtn.disabled = false;
    textarea.focus();
  }

  function close() {
    overlay.classList.add('hfb-hidden');
  }

  btn.addEventListener('click', open);
  closeBtn.addEventListener('click', close);
  overlay.addEventListener('click', function(e) {
    if (e.target === overlay) close();
  });

  submitBtn.addEventListener('click', function() {
    var text = textarea.value.trim();
    if (!text) {
      status.textContent = 'Le commentaire ne peut pas etre vide.';
      return;
    }

    submitBtn.disabled = true;
    status.textContent = 'Envoi...';

    var headers = {'Content-Type': 'application/json'};
    if (tokenKey) {
      var token = localStorage.getItem(tokenKey);
      if (token) headers['Authorization'] = 'Bearer ' + token;
    }

    fetch(base + '/submit', {
      method: 'POST',
      headers: headers,
      body: JSON.stringify({text: text, page_url: location.href})
    })
    .then(function(resp) {
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      return resp.json();
    })
    .then(function() {
      status.textContent = 'Merci !';
      textarea.value = '';
      setTimeout(close, 1500);
    })
    .catch(function(err) {
      status.textContent = 'Erreur : ' + err.message;
      submitBtn.disabled = false;
    });
  });

  // ESC to close
  document.addEventListener('keydown', function(e) {
    if (e.key === 'Escape' && !overlay.classList.contains('hfb-hidden')) close();
  });
})();
