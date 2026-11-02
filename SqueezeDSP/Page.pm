package Plugins::SqueezeDSP::Page;
# The SqueezeDSP page (docs/fork-plan.md, round 2): one player's signal path, its processing
# speed, and the controls that decide how it is fed (DSP, output, Upsample, preamp, balance,
# width, presets). The equaliser, filters and the rest stay on the classic page, linked from here.
#
# A RAW HANDLER, so the bytes below are the whole document: no template and no skin. The classic
# page is a template inside the classic skin's settings shell, which Material then frames, and
# that shell's custom-select widgets are what showed the wrong value after a reload (ledger, THE
# DROPDOWN SHOWS ITS FIRST OPTION). Here a select is a plain select. The recipe is HQPlayer
# Bridge's Live page (Live.pm there), and its two traps hold here too:
#   - a raw handler owns its STATUS CODE and its FRAMING: LMS fills in neither, so a missing
#     code goes out as "HTTP/1.1  " and a missing Content-Length leaves the browser waiting
#     for the end of a kept-alive connection;
#   - the version is handed in at init, never fetched from Plugin.pm at request time: a page
#     module that calls into Plugin.pm without loading it dies part way through the handler.
# The page itself is a NON-interpolating heredoc with @@NAME@@ placeholders, so nothing in its
# script is Perl's before it is JavaScript's. The script and the stylesheet are static files
# beside the classic page's (HTML/EN/plugins/SqueezeDSP/live/), which LMS serves with a week's
# cache, so their URLs carry the version and the server's start time.
use strict;
use warnings;

use Encode ();
use JSON::XS ();

use constant PATH => '/squeezedsp';

my $VERSION = '';
my $BOOT    = 0;
our $PAGE;    # the document, at the end of this file

# Every label the page shows, by the name its script uses
my %LABELS = (
    title       => 'PLUGIN_SQUEEZEDSP_DISPLAYNAME',
    dsp         => 'PLUGIN_SQUEEZEDSP_LIVE_DSP',
    dspHelp     => 'PLUGIN_SQUEEZEDSP_BYPASS_HELP',
    noDsp       => 'PLUGIN_SQUEEZEDSP_NO_DSP_BELOW_FLAC',
    dspOffNote  => 'PLUGIN_SQUEEZEDSP_LIVE_DSP_OFF',
    output      => 'PLUGIN_SQUEEZEDSP_OUTPUT',
    outputHelp  => 'PLUGIN_SQUEEZEDSP_OUTPUT_HELP',
    pcmNeeds    => 'PLUGIN_SQUEEZEDSP_PCM_NEEDS_HEADER',
    reducedLimit => 'PLUGIN_SQUEEZEDSP_REDUCED_LIMIT',
    reducedApp  => 'PLUGIN_SQUEEZEDSP_REDUCED_APP',
    upsample    => 'PLUGIN_SQUEEZEDSP_UPSAMPLE',
    upHelp      => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_HELP',
    phase       => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_PHASE',
    precision   => 'PLUGIN_SQUEEZEDSP_LIVE_PRECISION',
    passband    => 'PLUGIN_SQUEEZEDSP_LIVE_PASSBAND',
    pbDefault   => 'PLUGIN_SQUEEZEDSP_LIVE_PASSBAND_DEFAULT',
    steep       => 'PLUGIN_SQUEEZEDSP_LIVE_STEEP',
    noSox       => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_SOX',
    noSoxr      => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_SOXR',
    noFfmpeg    => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_FFMPEG',
    noMax       => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_MAX',
    noHook      => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_HOOK',
    noUpReduced => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_REDUCED',
    targets     => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_TARGETS',
    target      => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_TARGET',
    fixed       => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_FIXED',
    max         => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_MAX',
    maxNone     => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_MAX_NONE',
    dsd         => 'PLUGIN_SQUEEZEDSP_DSD',
    dsdHelp     => 'PLUGIN_SQUEEZEDSP_DSD_HELP',
    dsdCutoff   => 'PLUGIN_SQUEEZEDSP_DSD_CUTOFF',
    dsdGain     => 'PLUGIN_SQUEEZEDSP_DSD_GAIN',
    dsdNone     => 'PLUGIN_SQUEEZEDSP_DSD_NONE',
    dsdDecoder  => 'PLUGIN_SQUEEZEDSP_LIVE_DSD_DECODER',
    dsdLowpass  => 'PLUGIN_SQUEEZEDSP_LIVE_DSD_LOWPASS',
    dsdNoLowpass => 'PLUGIN_SQUEEZEDSP_LIVE_DSD_NO_LOWPASS',
    preamp      => 'PLUGIN_SQUEEZEDSP_PREAMP',
    preampHelp  => 'PLUGIN_SQUEEZEDSP_PREAMP_HELP',
    balance     => 'PLUGIN_SQUEEZEDSP_BALANCE',
    balanceHelp => 'PLUGIN_SQUEEZEDSP_BALANCE_HELP',
    width       => 'PLUGIN_SQUEEZEDSP_WIDTH',
    widthHelp   => 'PLUGIN_SQUEEZEDSP_WIDTH_HELP',
    apply       => 'PLUGIN_SQUEEZEDSP_LIVE_APPLY',
    presets     => 'PLUGIN_SQUEEZEDSP_HTML_PRESETS',
    load        => 'PLUGIN_SQUEEZEDSP_LIVE_LOAD',
    saveAs      => 'PLUGIN_SQUEEZEDSP_LIVE_SAVE_AS',
    del         => 'PLUGIN_SQUEEZEDSP_LIVE_DELETE',
    delAsk      => 'PLUGIN_SQUEEZEDSP_LIVE_DELETE_ASK',
    noPresets   => 'PLUGIN_SQUEEZEDSP_LIVE_NO_PRESETS',
    presetName  => 'PLUGIN_SQUEEZEDSP_LIVE_PRESET_NAME',
    path        => 'PLUGIN_SQUEEZEDSP_LIVE_PATH',
    speed       => 'PLUGIN_SQUEEZEDSP_LIVE_SPEED',
    realtime    => 'PLUGIN_SQUEEZEDSP_LIVE_REALTIME',
    slowest     => 'PLUGIN_SQUEEZEDSP_LIVE_SLOWEST',
    cpu         => 'PLUGIN_SQUEEZEDSP_LIVE_CPU',
    measuring   => 'PLUGIN_SQUEEZEDSP_LIVE_MEASURING',
    noProc      => 'PLUGIN_SQUEEZEDSP_LIVE_NO_PROC',
    notInPath   => 'PLUGIN_SQUEEZEDSP_LIVE_NOT_IN_PATH',
    finished    => 'PLUGIN_SQUEEZEDSP_LIVE_FINISHED',
    nextTrack   => 'PLUGIN_SQUEEZEDSP_LIVE_NEXT_TRACK',
    stopped     => 'PLUGIN_SQUEEZEDSP_LIVE_STOPPED',
    paused      => 'PLUGIN_SQUEEZEDSP_LIVE_PAUSED',
    source      => 'PLUGIN_SQUEEZEDSP_LIVE_SOURCE',
    stages      => 'PLUGIN_SQUEEZEDSP_LIVE_STAGES',
    float       => 'PLUGIN_SQUEEZEDSP_LIVE_FLOAT',
    more        => 'PLUGIN_SQUEEZEDSP_LIVE_MORE',
    back        => 'PLUGIN_SQUEEZEDSP_LIVE_BACK',
    noPlayer    => 'PLUGIN_SQUEEZEDSP_LIVE_NO_PLAYER',
    saved       => 'PLUGIN_SQUEEZEDSP_LIVE_SAVED',
    saveFailed  => 'PLUGIN_SQUEEZEDSP_LIVE_SAVE_FAILED',
    off         => 'PLUGIN_SQUEEZEDSP_LIVE_OFF',
    bypassed    => 'PLUGIN_SQUEEZEDSP_LIVE_UP_BYPASSED',
    dsdUp       => 'PLUGIN_SQUEEZEDSP_LIVE_UP_DSD',
);

sub labels { return {%LABELS} }

sub init {
    my ( $class, $version ) = @_;
    $VERSION = defined $version ? $version : '';
    $BOOT    = time();
    Slim::Web::Pages->addRawFunction( qr{^/squeezedsp/?$}, \&_handler );
    return;
}

sub _handler {
    my ( $httpClient, $response ) = @_;
    return unless $httpClient && $httpClient->connected;

    my $body = page();
    $body = Encode::encode_utf8($body) if utf8::is_utf8($body);    # octets on the wire, counted below

    $response->code(200);
    $response->content_type('text/html; charset=utf-8');
    $response->header( 'Cache-Control' => 'no-cache, no-store, must-revalidate' );
    $response->header( Pragma          => 'no-cache' );
    $response->content_length( length $body );

    Slim::Web::HTTP::addHTTPResponse( $httpClient, $response, \$body );
    return;
}

# The document: the labels in the server's language, as JSON the script reads; the version and
# the cache key for the static files
sub page {
    my %L = map { $_ => Slim::Utils::Strings::string( $LABELS{$_} ) } keys %LABELS;
    my $json = JSON::XS->new->ascii->canonical->encode( { strings => \%L, version => $VERSION } );
    $json =~ s{</}{<\\/}g;    # never a closing tag inside the script element
    my $v = _esc("$VERSION-$BOOT");
    my $html = $PAGE;
    $html =~ s/\@\@TITLE\@\@/_esc( $L{title} )/ge;
    $html =~ s/\@\@VERSION\@\@/_esc($VERSION)/ge;
    $html =~ s/\@\@V\@\@/$v/g;
    $html =~ s/\@\@DATA\@\@/$json/;
    return $html;
}

sub _esc {
    my $s = shift // '';
    $s =~ s/&/&amp;/g;
    $s =~ s/</&lt;/g;
    $s =~ s/>/&gt;/g;
    $s =~ s/"/&quot;/g;
    return $s;
}

$PAGE = <<'HTML';
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>@@TITLE@@</title>
<link rel="stylesheet" href="/material/html/font/font.css">
<script>
// Material's theme, read the way the classic skin's settings header reads it: Material records
// the choice in localStorage on this same origin. 'darker' is dark, 'auto' follows the system, a
// '-colored' or '-standard' suffix is a variant, a 'user:' theme lives under its own path.
(function () {
    var theme, col;
    try {
        theme = localStorage.getItem('lms-material::theme');
        col   = localStorage.getItem('lms-material::color');
    } catch (e) {}
    if (!theme || theme === 'darker') { theme = 'dark'; }
    if (theme === 'auto') {
        theme = (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark';
    }
    var parts = theme.split('-'), last = parts[parts.length - 1];
    if (parts.length > 1 && (last === 'colored' || last === 'standard')) { parts.pop(); }
    var name = parts.join('-');
    var isLight = theme.indexOf('light') === 0 || theme.indexOf('/light/') >= 0;
    function css(href) { var l = document.createElement('link'); l.rel = 'stylesheet'; l.href = href; document.head.appendChild(l); }
    css(name.indexOf('user:') === 0 ? '/material/usertheme/' + name.substring(5) : '/html/css/themes/' + (isLight ? 'light' : name) + '.min.css');
    if (col) { css(col.indexOf('user:') === 0 ? '/material/usercolor/' + col.substring(5) : '/html/css/colors/' + col + '.min.css'); }
    document.documentElement.className = isLight ? 'lt' : 'dk';
}());
</script>
<link rel="stylesheet" href="/plugins/SqueezeDSP/live/sqdsp_live.css?v=@@V@@">
</head>
<body>
<div class="wrap">
  <div class="top">
    <h1>@@TITLE@@</h1>
    <span class="who" id="who"></span>
    <span class="sub">@@VERSION@@</span>
    <span class="dot" id="dot"></span>
    <span class="flash" id="flash"></span>
    <button type="button" id="back"></button>
  </div>
  <div class="err" id="err"></div>
  <div class="pick" id="pick"></div>
  <div id="app"></div>
</div>
<script>var SQDSP = @@DATA@@;</script>
<script src="/plugins/SqueezeDSP/live/sqdsp_live.js?v=@@V@@"></script>
</body>
</html>
HTML

1;
