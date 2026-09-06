/* The favicon follows the palette. This is the page's only JavaScript, and it
   exists because a data-URI favicon cannot animate itself: browsers rasterise
   an SVG favicon once and ignore any animation inside it. It reads the colour
   the CSS animation is actually showing on the mark rather than keeping a
   second clock, so the two can never drift; and it does nothing at all for a
   visitor who has asked for reduced motion. */
(function(){
  try {
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    var link = document.querySelector('link[rel=icon]');
    var mark = document.querySelector('.mark');
    if (!link || !mark) return;
    var last = '';
    var paint = function(){
      var c = getComputedStyle(mark).color;
      if (!c || c === last) return;
      last = c;
      link.href = 'data:image/svg+xml,' + encodeURIComponent(
        '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">' +
        '<rect x="1" y="1" width="7" height="7" fill="' + c + '"/>' +
        '<rect x="8" y="8" width="7" height="7" fill="' + c + '"/></svg>');
    };
    paint();
    setInterval(paint, 1000);
  } catch (e) { /* a static favicon is a fine outcome */ }
})();
