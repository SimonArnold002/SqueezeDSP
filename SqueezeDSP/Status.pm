package Plugins::SqueezeDSP::Status;
# The live signal path of one player, for the SqueezeDSP page (docs/fork-plan.md, round 2): what
# LMS is playing, what the running pipeline does with it, and how fast that pipeline works.
#
# The pipeline is read from the processes LMS is running for the player, not from its settings,
# so the page shows what RUNS: the precision soxr was handed, the phase sox was handed, the rate
# the engine was told. LMS starts a rule through /bin/sh (open2 or a piped open), so the stages
# are that shell's children; the engine is the one whose command line names the player
# (--Clientid=<id>). Except for a LOCAL FILE: LMS ends that command with ' &' (no socket, so
# Song.pm:573 passes no noPipe and TranscodingHelper.pm:663-665 adds it), the shell starts the
# pipeline in the background and exits, and init takes the stages over. They are out of LMS's
# tree but still in its session, and their order is in their pipes (rig 2026-10-25, the Lounge).
#
# REAL TIME SPEED is how many times faster than real time the engine runs flat out, as the engine itself measures and
# reports it (LyrionDSPProcessAudio reportSpeed, read by _engineSpeed): one figure on every platform. At a stream's
# start its FILL (foxSpeed.Fill, 0.1.72.35): the audio written while the player's buffer filled and LMS took
# everything, over the time taken, its waits for the player left out. Since 0.1.72.46 also while it plays (Simon
# 2026-11-06: "a user needs to see the limits of their machine"): the engine keeps up to 3 s of its output ahead of LMS
# and refills it in runs, flat out, each timed where the audio enters it (foxSpeed.Runs), the figure the runs of the
# last 10 s; so a heavier track or a busier server shows within a run or two (ledger THE SPEED SHOWN IS ONE START'S
# FILL). Not on Windows, where LMS's socketwrapper ends a pipeline's processes 2 s after its first one: there the fill
# alone, and a gapless change, whose engine never runs flat out, keeps the player's last fill (%fill); elsewhere that
# bridges only the second before its own. Nothing measured from outside the engine is its speed: CPU understates an
# engine that spreads its stages over cores (on the M3 at DSD512 it ran 4.1x real time where its CPU read 1.8x), and the
# output's timing and each stage's own time overstate it (ledger THE PAGE'S SPEED WAS PER CPU-SECOND). The engine also
# names its busiest stage by its own work, the one to speed up.
#
# STAGES: what each process of the pipeline costs, its CPU per second of the audio processed. Audio processed is
# counted from the bytes that crossed the engine's output pipe (the stage after it reads exactly those), so it is the
# same for every stage; CPU is /proc's utime + stime, both counted from each stage's start, so the figures settle over
# a track. Never CPU over wall time, which counts the buffer filling at a track's start (measure). The page shows each
# stage's as a share of the whole CPU, its every core (cores, /proc/stat: Simon 2026-11-05, "it needs to be overall
# cpu performance"). Not a speed (0.1.72.46: the status names none; a stage's audio per CPU-second read as one was
# how 1.6x got quoted against the page's 3.7x). Linux only (/proc); elsewhere the engine's own report alone
# (_reportOnly).
#
# A pipeline lives while LMS STREAMS its track, not while the player plays it. Once the whole track
# is with the player (the FiiO buffers 15-20 s of 384k) its pipeline exits, and the next track's
# starts a few seconds before the change (rig 2026-10-24). So the report remembers each player's last
# measurement with the STREAM it was taken from: a finished track keeps its final figures, and a
# pipeline running for the song after the playing one is said to be the next track's. Not with the
# song: a seek re-opens the SAME song (StreamingController.pm:1092-1142 hands _Stream the playing
# song), and so does every DSP change saved here, so a song can be one stream through us and the
# next one straight past us. LMS makes one stream controller per open (Song.pm:665) and drops or
# replaces it at the next open, a stop or a paused stream's close.
use strict;
use warnings;
use File::Spec;
use JSON::XS ();
use List::Util qw(max);
use Scalar::Util qw(refaddr weaken);

# tests point these at a fixture tree and at a made-up LMS pid
our $PROC   = '/proc';
our $LMSPID = $$;
our $TICKS;    # clock ticks per second; sysconf(_SC_CLK_TCK), which is 100 on Linux

# client id -> { engine => pid, pids => [ the pipeline's pids, in fork order ] }
my %cache;

# client id -> { stream => LMS's stream controller for the stream measured (a WEAK reference: it is
# never kept alive, and a freed one can never match a new one at the same address), m => its last
# measurement }
my %last;

# client id -> { speed => the engine's last fill figure, track => the title of the track it was measured at }: shown
# for a pipeline that has none of its own, as a gapless change's never runs flat out (report)
my %fill;

sub clk_tck {
    return $TICKS //= ( eval { require POSIX; POSIX::sysconf( POSIX::_SC_CLK_TCK() ) } || 100 );
}

# Whether this server can be read at all (a /proc with an uptime)
sub available { return ( -d $PROC && -r "$PROC/uptime" ) ? 1 : 0 }

# The server's CPUs, as a CPU monitor counts them (htop's bars): /proc/stat's cpu0, cpu1, ... lines, not the
# total "cpu" line; undef when it cannot be read
sub cores {
    my $s = _slurp("$PROC/stat") // return undef;
    my $n = () = $s =~ /^cpu\d+\s/mg;
    return $n || undef;
}

sub _slurp {
    my $f = shift;
    open( my $fh, '<', $f ) or return undef;
    local $/;
    my $s = <$fh>;
    close $fh;
    return $s;
}

sub _argv {
    my $pid = shift;
    my $s = _slurp("$PROC/$pid/cmdline");
    return () unless defined $s && length $s;
    $s =~ s/\0\z//;
    return split /\0/, $s;
}

# state, ppid, session, CPU (utime + stime, in ticks) and start (ticks after boot) of one process
sub _stat {
    my $pid = shift;
    my $s = _slurp("$PROC/$pid/stat");
    return undef unless defined $s;
    my $close = rindex( $s, ')' );    # the command name may hold ')' or spaces
    return undef if $close < 0;
    my @f = split ' ', substr( $s, $close + 1 );
    return undef unless @f > 19;
    return { state => $f[0], ppid => $f[1], session => $f[3], cpu => $f[11] + $f[12], start => $f[19] };
}

sub _io {
    my $pid = shift;
    my $s = _slurp("$PROC/$pid/io");
    return {} unless defined $s;
    my %io = $s =~ /^(\w+):\s*(\d+)/mg;
    return \%io;
}

sub _pids {
    opendir( my $dh, $PROC ) or return ();
    my @p = grep { /^\d+$/ } readdir $dh;
    closedir $dh;
    return @p;
}

sub _isEngineOf {
    my ( $pid, $id ) = @_;
    my $want = '--clientid=' . lc $id;
    return scalar grep { lc($_) eq $want } _argv($pid);
}

# The children of one process, in fork order, which for a pipeline is its order left to right.
# The kernel keeps that list itself (task/<pid>/children, newest last) where it is built with
# CONFIG_PROC_CHILDREN, as Debian's and Ubuntu's kernels are; without it every process's parent
# is read, ordered by start time and then pid. The second answer says which of the two it was.
sub _children {
    my $ppid = shift;
    my $s = _slurp("$PROC/$ppid/task/$ppid/children");
    return ( [ split ' ', $s ], 1 ) if defined $s;
    my %st = map { my $x = _stat($_); ( $x && $x->{ppid} == $ppid ) ? ( $_ => $x ) : () } _pids();
    return ( [ sort { $st{$a}{start} <=> $st{$b}{start} || $a <=> $b } keys %st ], 0 );
}

# Where to look for an engine: LMS's own children and theirs, since LMS starts every pipeline
# (Pipeline.pm's open2, or a piped open, both through /bin/sh). A dozen processes, not every one
# on the machine. Without the kernel's list, every process.
sub _candidates {
    my $s = _slurp("$PROC/$LMSPID/task/$LMSPID/children");
    return _pids() unless defined $s;
    my @kids = split ' ', $s;
    return ( @kids, map { @{ ( _children($_) )[0] } } @kids );
}

# Every process in LMS's session, pid => _stat. A local file's pipeline outlives the shell that
# started it in the background, but not LMS's session; an engine run by hand from another login
# is in that login's session.
sub _session {
    my $lms = _stat($LMSPID) or return {};
    my %s;
    for my $pid ( _pids() ) {
        my $x = _stat($pid);
        $s{$pid} = $x if $x && $x->{session} == $lms->{session};
    }
    return \%s;
}

# the pipe a process has on one of its fds (its inode), or undef
sub _pipe {
    my ( $pid, $fd ) = @_;
    my $l = readlink("$PROC/$pid/fd/$fd");
    return ( defined $l && $l =~ /^pipe:\[(\d+)\]$/ ) ? $1 : undef;
}

# A pipeline whose shell has gone, in order, out of the pool it was handed over with: the stage
# before a stage writes (fd 1) the pipe it reads (fd 0), the stage after reads the one it writes.
# LMS reads the last one on some other fd, so the walk stops there.
sub _chain {
    my ( $engine, @pool ) = @_;
    my ( %writes, %reads );
    for my $p (@pool) {
        my $o = _pipe( $p, 1 );
        my $i = _pipe( $p, 0 );
        $writes{$o} //= $p if defined $o;
        $reads{$i}  //= $p if defined $i;
    }
    my @chain = ($engine);
    my %seen = ( $engine => 1 );
    while ( defined( my $i = _pipe( $chain[0], 0 ) ) ) {
        my $p = $writes{$i};
        last unless defined $p && !$seen{$p}++;
        unshift @chain, $p;
    }
    while ( defined( my $o = _pipe( $chain[-1], 1 ) ) ) {
        my $p = $reads{$o};
        last unless defined $p && !$seen{$p}++;
        push @chain, $p;
    }
    return @chain;
}

# The pids of this player's running pipeline, in pipeline order (empty when none runs). The
# newest engine wins, should two overlap at a track change. A pipeline started without a shell
# has LMS itself as the engine's parent, and then the engine is the whole of it. The engine is
# remembered between polls (and checked by its start time, so a reused pid is not taken for it,
# and by its parent, so a shell that has since exited sends it round again); under LMS's shell the
# stages are read again on each poll where the kernel lists them, so a stage still being forked
# at the first poll is not missed for the rest of the track. A pipeline whose shell has gone (a
# local file's, LMS's ' &') is looked for in LMS's session and its order read from its pipes.
# $streaming 0: LMS holds no stream for the player, so none is looked for (review 2026-11-01: the
# search reads every process, on each poll of an idle player). LMS starts a pipeline only by opening
# a stream and holds the stream from then on (StreamingController.pm:1250, :1342); it drops it only
# by closing it (:465, :612, :1245). undef: not known, looked for as before.
sub pipelinePids {
    my ( $id, $streaming ) = @_;
    my $c = $cache{$id};
    if ($c) {
        my $st = _isEngineOf( $c->{engine}, $id ) ? _stat( $c->{engine} ) : undef;
        undef $c unless $st && $st->{state} ne 'Z' && $st->{start} == $c->{start} && $st->{ppid} == $c->{ppid};
    }
    unless ($c) {
        delete $cache{$id};
        return () if defined $streaming && !$streaming;
        my %start;
        my @found = grep { _isEngineOf( $_, $id ) } _candidates();
        @found = grep { _isEngineOf( $_, $id ) } keys %{ _session() } unless @found;
        for my $pid (@found) {
            my $st = _stat($pid) or next;
            next if $st->{state} eq 'Z';
            $start{$pid} = $st;
        }
        return () unless %start;
        my ($engine) = sort { $start{$b}{start} <=> $start{$a}{start} || $b <=> $a } keys %start;
        $c = $cache{$id} = { engine => $engine, start => $start{$engine}{start}, ppid => $start{$engine}{ppid} };
    }
    my $ppid = $c->{ppid};
    return ( $c->{engine} ) if !$ppid || $ppid == $LMSPID;
    my $shell = _stat($ppid);
    if ( $shell && $shell->{ppid} == $LMSPID ) {
        if ( !$c->{pids} || $c->{kernel} ) {
            my ( $pids, $kernel ) = _children($ppid);
            @$c{qw(pids kernel)} = ( $pids, $kernel );
        }
        return ( grep { $_ == $c->{engine} } @{ $c->{pids} } ) ? @{ $c->{pids} } : ( $c->{engine} );
    }
    # the shell has gone: its stages, handed over together, are this one's siblings in LMS's session
    $c->{pids} //= do {
        my $s = _session();
        [ _chain( $c->{engine}, grep { $s->{$_}{ppid} == $ppid } keys %$s ) ];
    };
    return @{ $c->{pids} };
}

# a player's remembered engine and measurement dropped (the tests start each case clean)
sub forget { delete $cache{ $_[0] }; delete $last{ $_[0] }; delete $fill{ $_[0] } }

# What a stage is, from its command line
sub _role {
    my ( $argv, $isEngine ) = @_;
    return 'dsp' if $isEngine;
    my $bin = lc( ( split m{/}, $argv->[0] // '' )[-1] );
    my $args = join( ' ', @$argv[ 1 .. $#$argv ] );
    if ( $bin eq 'ffmpeg' ) {
        # the DSD rule's ffmpeg (TemplateConfig.pm $DSD) decodes a DSF or DFF file, and resamples it too
        # with soxr (Upsample::dsdFilter); LMS types DSD by those extensions alone
        return 'dsd'     if grep { $argv->[ $_ - 1 ] eq '-i' && $argv->[$_] =~ /\.(?:dsf|dff)$/i } 2 .. $#$argv;
        return 'soxr'    if $args =~ /aresample=\d+:resampler=soxr/;
        return 'level'   if $args =~ /-af volume=\d/;
        return 'ffmpeg';
    }
    if ( $bin eq 'sox' ) {
        # the DSD rule's sox on a server with no ffmpeg (TemplateConfig.pm _dsdSox) decodes and resamples it
        return 'dsd'     if grep { $argv->[ $_ - 1 ] eq '-t' && $argv->[$_] =~ /^(?:dsf|dff)$/ } 2 .. $#$argv;
        return 'soxrate' if $args =~ / rate (?:-[A-Za-z][^ ]*(?: \d+(?:\.\d+)?)? )*\d+$/ && $args =~ / -d \d+/;
        # a reduced stream's Vorbis encoder (TemplateConfig.pm %REDUCED)
        return 'ogg-enc' if $args =~ / -t ogg -C \S+ -(?: |$)/;
        return 'sox';
    }
    # the DSD rule's dsdplay (DSDPlayer's) on a server with no ffmpeg (TemplateConfig.pm _dsdPlay) decodes and resamples it
    return 'dsd'      if $bin eq 'dsdplay';
    # a reduced stream's MP3 and Opus encoders; lame also decodes an MP3 for the engine (--decode)
    return 'mp3-enc'  if $bin eq 'lame' && $args !~ /(?:^| )--decode(?: |$)/;
    return 'opus-enc' if $bin eq 'opusenc';
    return 'flac-dec' if $bin eq 'flac' && $args =~ /(?:^| )-d/;
    return 'flac-enc' if $bin eq 'flac';
    # PCM mode's length filter (WavLength.pm): the Perl LMS runs on, given the filter with -e
    return 'wavlength' if $bin =~ /^perl/ && ( $argv->[1] // '' ) eq '-e' && index( $argv->[2] // '', 'RIFF' ) >= 0;
    return $bin;
}

# The engine's own arguments (--samplerate=96000 and the like), lower-cased names
sub _engineArgs {
    my $argv = shift;
    my %a;
    for (@$argv) { $a{ lc $1 } = $2 if /^--(\w+)=(.*)$/ }
    return \%a;
}

# What the resampler stage was handed: soxr through ffmpeg, or sox's rate. DSD's soxr is in its
# decoding ffmpeg (dsd => 1); with no ffmpeg on the server it is in DSDPlayer's dsdplay (-R vL: soxr's
# very high quality, 28-bit, linear phase), or its rate is in its decoding sox (sox's -v quality, linear
# phase); a DSD rule on ffmpeg's own resampler has no resampler stage the page can read.
sub _resampler {
    my @stages = @_;
    for my $s (@stages) {
        my $args = join( ' ', @{ $s->{argv} } );
        # our engine's own DSD decode (TemplateConfig _dsdEngine, 0.3.13): one process reads the DSF and decodes it to
        # --samplerate with its own linear-phase filters, then runs the DSP; its low-pass and gain are the player's
        # settings unless its command line gives them (report fills them in: _dsdSettings)
        if ( $s->{role} eq 'dsp' ) {
            my $a = _engineArgs( $s->{argv} );
            if ( uc( $a->{formatin} // '' ) eq 'DSD' && ( $a->{samplerate} // '' ) =~ /^(?:\d+|native)$/i ) {
                # native (the native round): no rate change, the DSP at the source's DSD rate (measure sets the target);
                # its integrator is the player's (report fills it in: _dsdSettings)
                my $native = $a->{samplerate} !~ /^\d+$/;
                return { tool => 'decoder', phase => 'linear', dsd => 1,
                         ( $native ? ( native => 1 ) : ( target => 0 + $a->{samplerate} ) ),
                         ( ( $a->{dsdcutoff} // '' ) =~ /^\d+(?:\.\d+)?$/ ? ( cutoff => 0 + $a->{dsdcutoff} ) : () ),
                         ( ( $a->{dsdgain} // '' ) =~ /^-?\d+(?:\.\d+)?$/ ? ( gain => 0 + $a->{dsdgain} ) : () ) };
            }
        }
        if ( ( $s->{role} eq 'soxr' || $s->{role} eq 'dsd' )
             && $args =~ /aresample=(\d+):resampler=soxr:precision=(\d+):cheby=(\d)(?::cutoff=([\d.]+))?/ ) {
            return { tool => 'soxr', phase => 'linear', target => 0 + $1, precision => 0 + $2, steep => 0 + $3,
                     passband => defined $4 ? 0 + $4 : 0, ( $s->{role} eq 'dsd' ? ( dsd => 1 ) : () ) };
        }
        if ( $s->{role} eq 'dsd' && $s->{argv}[0] =~ m{(?:^|/)sox$}i && $args =~ / rate -v -L (\d+)$/ ) {
            return { tool => 'sox', phase => 'linear', target => 0 + $1, passband => 0, steep => 0, dsd => 1 };
        }
        if ( $s->{role} eq 'dsd' && $s->{argv}[0] =~ m{(?:^|/)dsdplay$}i && join( ' ', map { $_ // '' } @{ $s->{argv} }[ 1, 2 ] ) eq '-R vL' ) {
            # its first -r: the file, a full path, comes last
            my ($r) = map { $s->{argv}[ $_ + 1 ] } grep { $s->{argv}[$_] eq '-r' } 3 .. $#{ $s->{argv} } - 1;
            return { tool => 'soxr', phase => 'linear', target => 0 + $r, precision => 28, passband => 0, steep => 0, dsd => 1 }
                if defined $r && $r =~ /^\d+$/;
        }
        if ( $s->{role} eq 'soxrate' && $args =~ / rate (.*) (\d+)$/ ) {
            my ( $o, $target ) = ( $1, 0 + $2 );
            my ($p) = $o =~ /-d (\d+)/;
            my ($b) = $o =~ /-B ([\d.]+)/;
            return { tool => 'sox', target => $target, precision => defined $p ? 0 + $p : undef,
                     phase => $o =~ /(?:^| )-M(?: |$)/ ? 'minimum' : $o =~ /(?:^| )-I(?: |$)/ ? 'intermediate' : 'linear',
                     passband => defined $b ? $b / 100 : 0, steep => ( $o =~ /(?:^| )-f(?: |$)/ && $o =~ /(?:^| )-t(?: |$)/ ) ? 1 : 0 };
        }
    }
    return undef;
}

# The converter to DSD (0.3.19): the engine upsamples a PCM input itself (--upsample), by the player's filters
# (--upfilter: one for a source at 48 kHz and under, then one for hi-res, the source's rate saying which ran; one value
# serves both, as the engine reads it; none or one it refuses, its defaults). Nothing ran for a source already there.
# The filter that ran, as the page names it: its family (kaiser, gauss, halfband, slow, nonring), length (Kaiser's and
# Gaussian's) and phase (none for Non-ringing). Going down a leaky family runs as Kaiser short in its phase, as the
# engine does it (foxUpsampler New).
my $FILTER = qr/(?:(?:short|long|xlong|gauss-(?:short|medium|long)|halfband|slow)-(?:lin|int|min)|nonring)/;
sub _engineUp {
    my ( $a, $rate ) = @_;
    return undef unless ( $a->{upsample} // '' ) =~ /^\d+$/ && uc( $a->{formatin} // '' ) ne 'DSD';
    return undef if defined $rate && $rate == $a->{upsample};
    my @f = map { lc } split /,/, ( $a->{upfilter} // '' ), -1;
    @f = ( $f[0] ) x 2 if @f == 1;
    @f = ( 'short-int', 'short-lin' ) unless @f == 2 && !grep { !/^$FILTER$/ } @f;
    my $f = ( defined $rate && $rate > 48000 ) ? $f[1] : $f[0];
    $f = 'short-' . ( $f =~ /-(lin|int|min)$/ ? $1 : 'lin' ) if defined $rate && $rate > $a->{upsample} && $f =~ /^(?:halfband|slow|nonring)\b/;
    my ( $fam, $len, $ph ) = $f eq 'nonring' ? ( 'nonring', undef, undef )
                           : $f =~ /^(gauss|halfband|slow)-(?:(short|medium|long)-)?(lin|int|min)$/ ? ( $1, $2, $3 )
                           : $f =~ /^(short|long|xlong)-(lin|int|min)$/ ? ( 'kaiser', $1, $2 ) : ();
    return { tool => 'engine', target => 0 + $a->{upsample}, filter => $f, family => $fam, len => $len,
             phase => defined $ph ? { lin => 'linear', int => 'intermediate', min => 'minimum' }->{$ph} : undef };
}

# The output stage: LMS's flac, or the engine writing WAV itself (PCM mode), or sox writing WAV or
# FLAC at its own rate (a D rule), or a reduced stream's encoder (reduced => 1): MP3 and Opus at a
# bitrate, at the rate their encoder picks (Opus always 48k), Ogg Vorbis at a quality, at the
# engine's rate or sox's on D. PCM mode's length filter after it changes no format, so `measure`
# hands this the last stage before the filter. Native DSD out: the engine writing a DSF stream
# itself, at the DSD rate and with the modulator it took ($dsf: _dsdOut).
sub _output {
    my ( $last, $engineArgs, $dsf ) = @_;
    my $rate = $engineArgs->{samplerate};
    my $args = join( ' ', @{ $last->{argv} || [] } );
    if ( $last->{role} eq 'mp3-enc' ) {
        my ($kbps) = $args =~ /(?:^| )(?:--abr|-b) (\d+)(?: |$)/;
        return { format => 'MP3', kbps => defined $kbps ? 0 + $kbps : undef, reduced => 1 };
    }
    if ( $last->{role} eq 'opus-enc' ) {
        my ($kbps) = $args =~ /(?:^| )--bitrate[ =](\d+)(?: |$)/;
        return { format => 'Opus', kbps => defined $kbps ? 0 + $kbps : undef, rate => 48000, reduced => 1 };
    }
    if ( $last->{role} eq 'ogg-enc' ) {
        my ($q)    = $args =~ / -t ogg -C (\S+) /;
        my ($down) = $args =~ / rate -v (\d+)\s*$/;
        return { format => 'Ogg Vorbis', quality => $q, rate => $down ? 0 + $down : $rate, reduced => 1 };
    }
    return { format => 'FLAC', bits => 24, rate => $rate } if $last->{role} eq 'flac-enc';
    if ( $last->{role} eq 'sox' ) {
        my ($down) = $args =~ / rate -v (\d+)\s*$/;
        return { format => ( $args =~ / -t flac / ? 'FLAC' : 'WAV' ), bits => 24, rate => $down ? 0 + $down : $rate };
    }
    return { format => 'DSF', dsd => ( $dsf || {} )->{rate}, mod => ( $dsf || {} )->{mod} }
        if $last->{role} eq 'dsp' && uc( $engineArgs->{formatout} // '' ) eq 'DSF';
    return { format => ( ( $engineArgs->{formatout} // '' ) eq 'FLC' ? 'FLAC' : 'WAV' ), bits => $engineArgs->{bitsout} // 24, rate => $rate }
        if $last->{role} eq 'dsp';
    return undef;
}

# The running pipeline of one player, measured: its stages (role, CPU), the engine's own speed, what it was
# told to do, and the audio it has processed (with no /proc: the engine's report alone, _reportOnly).
# $fallbackRate is LMS's view of the track, for the one
# route whose engine command carries no rate (the engine reading a WAV or AIFF file itself), and
# for native DSD out the source's DSD rate, which $dsdOut (report's _dsdOut) turns into the DSD
# rate and modulator the engine takes from the player's settings.
sub measure {
    my ( $id, $fallbackRate, $streaming, $dsdOut ) = @_;
    return _reportOnly($id) unless available();
    my @pids = pipelinePids( $id, $streaming );
    return { running => 0 } unless @pids;

    my $tck = clk_tck();
    my ( @stages, $engineIdx );
    for my $pid (@pids) {
        my $st = _stat($pid) or next;
        next if $st->{state} eq 'Z';
        my @argv = _argv($pid) or next;
        my $isEngine = _isEngineOf( $pid, $id );
        $engineIdx = scalar @stages if $isEngine;
        push @stages, { pid => $pid, argv => \@argv, role => _role( \@argv, $isEngine ), cpu => $st->{cpu} / $tck,
                        start => $st->{start} / $tck, io => _io($pid) };
    }
    return { running => 0 } unless defined $engineIdx;

    my $e = $stages[$engineIdx];
    my $a = _engineArgs( $e->{argv} );
    # native DSD (--samplerate=native, the native round): the DSP at the source's DSD rate, LMS's view of the track
    my $native = lc( $a->{samplerate} // '' ) eq 'native';
    my $rate = $native ? $fallbackRate : $a->{samplerate} // $fallbackRate;
    my $ch = $a->{channels} // 2;
    # the rate the DSP runs at: the converter's (--upsample: the engine upsamples itself, 0.3.19), else the input's
    my $dspRate = ( $a->{upsample} // '' ) =~ /^\d+$/ && uc( $a->{formatin} // '' ) ne 'DSD' ? 0 + $a->{upsample} : $rate;

    # the audio that crossed the engine's output pipe: the stage after it read exactly those bytes
    # (the engine's own count also holds its log writes); a WAV output is 24-bit at the rate the engine writes, the
    # DSP's: with --upsample (Upsample's PCM since 0.1.72.37) not the input's --samplerate
    my $audio;
    my $outWav = ( $a->{formatout} // 'WAV' ) eq 'WAV';
    if ( $outWav && $dspRate && $ch ) {
        my $bps = $dspRate * $ch * ( ( $a->{bitsout} // 24 ) / 8 );
        my $next = $stages[ $engineIdx + 1 ];
        my $bytes = $next ? $next->{io}{rchar} : $e->{io}{wchar};
        $audio = max( 0, ( $bytes // 0 ) - 44 ) / $bps if defined $bytes;
    }
    # FLACENGINE (the engine encodes): count what went INTO it, if it read raw PCM from a stage
    if ( !defined $audio && ( $a->{formatin} // '' ) eq 'PCM' && $rate && $ch && $a->{bitsin} && $engineIdx > 0 ) {
        my $bytes = $stages[ $engineIdx - 1 ]{io}{wchar};
        $audio = $bytes / ( $rate * $ch * ( $a->{bitsin} / 8 ) ) if defined $bytes;
    }
    # native DSD out (TemplateConfig template_DSF): the engine is the last stage and writes the DSF itself, its header
    # (92 bytes) then one bit per channel per sample at the DSD rate out, so its own count is the audio (as PCM mode's)
    my $dsf;
    if ( uc( $a->{formatout} // '' ) eq 'DSF' ) {
        $dsf = ref $dsdOut eq 'CODE' ? $dsdOut->( $fallbackRate, $a ) : undef;
        my $bytes = $e->{io}{wchar};
        $audio = max( 0, $bytes - 92 ) / ( $dsf->{rate} * $ch / 8 )
            if $dsf && $dsf->{rate} && $ch && defined $bytes && $engineIdx == $#stages;
    }

    # the CPU figure is the cost at real time: every stage's CPU per second of the audio processed, as a share of one
    # core (the stages added up, which a CPU monitor shows spread over cores). Not over wall time: at a track's start
    # the pipeline runs flat out until the player's buffer is full (ManCave 2026-11-03: 134 s of audio in its first
    # 34 s), so CPU over wall time read 81-89% there where playing costs 21%, and a pipeline that ends once the whole
    # track is with the player never settles to it (Simon: "it was saying 89% of one cpu core in our signal path").
    # The page no longer shows this total (0.1.72.38): its Stages row gives each stage's cost as a share of the whole
    # CPU, every core (cores)
    my $cpuAll = 0;
    $cpuAll += $_->{cpu} for @stages;

    my $es = _engineSpeed( $id, $e->{pid} ) || {};
    my @out = map { { role => $_->{role}, cpu => sprintf( '%.2f', $_->{cpu} ) + 0 } } @stages;
    my $res = _resampler(@stages) // _engineUp( $a, $rate );
    $res->{target} = $rate if $res && $res->{native} && defined $rate && $rate =~ /^\d+$/;

    return {
        running   => 1,
        audio     => defined $audio ? sprintf( '%.1f', $audio ) + 0 : undef,
        cpu       => ( defined $audio && $audio > 0 ) ? sprintf( '%.1f', 100 * $cpuAll / $audio ) + 0 : undef,
        cores     => cores(),
        realtime  => $es->{speed},
        busiest   => $es->{stage},
        stage_ms  => $es->{stages},
        stages    => \@out,
        resampler => $res,
        dsp       => { rate => $dspRate, bitsin => $a->{bitsin}, float => ( ( $a->{bitsin} // 0 ) == 32 ? 1 : 0 ) },
        output    => _output( ( grep { $_->{role} ne 'wavlength' } @stages )[-1], { %$a, samplerate => $dspRate }, $dsf ),
    };
}

# _speedReport: the engine's report for a player (LyrionDSPProcessAudio reportSpeed), the file it writes in the
# plugin's temp folder under its player's id, cleaned as the engine cleans it (sanitizeClientID), from its start and
# every second while it runs: ( the report, its age in seconds ), or nothing.
sub _speedReport {
    my ($id) = @_;
    my $dir = $Plugins::SqueezeDSP::Plugin::pluginTempDataDir;
    return unless defined $dir && length $dir;
    ( my $clean = $id ) =~ tr/:-/__/;
    my $file = File::Spec->catfile( $dir, "speed_$clean.json" );
    my $raw = _slurp($file);
    return unless defined $raw;
    my $j = eval { JSON::XS::decode_json($raw) };
    return unless ref $j eq 'HASH';
    my $mtime = ( stat $file )[9];
    return ( $j, defined $mtime ? time - $mtime : undef );
}

# _engineSpeed: the engine's own speed from its report, only the engine measured's (its pid, so never a run before it):
# _figures, or undef.
sub _engineSpeed {
    my ( $id, $pid ) = @_;
    return undef unless defined $pid;
    my ($j) = _speedReport($id);
    return undef unless $j && ( $j->{pid} // 0 ) == $pid;
    return _figures($j);
}

# _figures: { speed => the report's audio over its time once that holds two seconds of audio (the runs' while it plays,
# else the fill's: the engine picks), stage => its busiest stage, "convolver 2" as "convolver", stages => each stage's
# own time }, any undef; undef with none (an engine that encodes FLAC itself measures nothing).
sub _figures {
    my ($j) = @_;
    my $speed = ( ( $j->{busy} // 0 ) > 0 && ( $j->{audio} // 0 ) >= 2 ) ? sprintf( '%.1f', $j->{audio} / $j->{busy} ) + 0 : undef;
    my $stage = ( defined $j->{stage} && !ref $j->{stage} && $j->{stage} =~ /^([a-z]+)/ ) ? $1 : undef;
    # (0.1.72.46) each stage's own time, ms of work a second of audio, the route's order (the engine's stages): for a
    # live test to read one stage alone (ledger THE AVX2 LOOPS); the page shows none of it
    my @ms = map { { name => $_->{name}, ms => $_->{ms} + 0 } }
      grep { ref $_ eq 'HASH' && defined $_->{name} && !ref $_->{name} && defined $_->{ms} && !ref $_->{ms} && $_->{ms} =~ /^\d+(?:\.\d+)?$/ }
      ( ref $j->{stages} eq 'ARRAY' ? @{ $j->{stages} } : () );
    return ( defined $speed || defined $stage || @ms ) ? { speed => $speed, stage => $stage, stages => ( @ms ? \@ms : undef ) } : undef;
}

# _reportOnly (0.1.72.46): a server with no /proc (macOS, Windows) cannot see the pipeline, so the engine's own report
# alone, while it is fresh: no older than $FRESH seconds (the engine rewrites it every second from its start; its pid
# cannot be checked here). Its speed and busiest stage; no stages, so the page draws no signal path rows (what each
# stage costs, the resampler, the output: /proc's).
our $FRESH = 3;

sub _reportOnly {
    my ($id) = @_;
    my ( $j, $age ) = _speedReport($id);
    return { running => 0 } unless $j && defined $age && $age <= $FRESH;
    my $f = _figures($j) || {};
    return { running => 1, realtime => $f->{speed}, busiest => $f->{stage}, stage_ms => $f->{stages} };
}

# What LMS is playing on this player, through its own status query, so a streamed track's title
# comes from its service as Material shows it: mode, title, artist, and LMS's view of the format.
# With a playlist index: that track instead (the one LMS streams ahead).
sub _now {
    my ( $client, $index ) = @_;
    my $r = eval { Slim::Control::Request::executeRequest( $client, [ 'status', $index // '-', 1, 'tags:aITo' ] ) };
    my $res = ( $r && eval { $r->getResults } ) || {};
    my $t = ( ref $res->{playlist_loop} eq 'ARRAY' && $res->{playlist_loop}[0] ) || {};
    my %n = ( mode => $res->{mode} // 'stop' );
    for ( [ title => 'title' ], [ artist => 'artist' ], [ type => 'type' ], [ rate => 'samplerate' ], [ bits => 'samplesize' ] ) {
        my ( $k, $from ) = @$_;
        $n{$k} = $t->{$from} if defined $t->{$from} && length $t->{$from};
    }
    return \%n;
}

# Whether LMS streamed this song to this player through one of our rules: it transcoded it, and
# the rule for the formats it matched (Song::open sets wantFormat, then the stream format) on this
# player runs the engine. Rule keys are <in>-<out>-<model>-<player>, a repeat -<n> (Configuration.pm
# syncCommandTable). For a track whose pipeline ended before the page saw it run: a short track,
# or the page opened near a track's end.
sub _ourRule {
    my ( $client, $song ) = @_;
    return 0 unless $song && eval { $song->_transcoded };
    my $in  = eval { $song->wantFormat } // '';
    my $out = eval { $song->streamformat } // '';
    return 0 unless length $in && length $out;
    my $id    = lc $client->id;
    my $tag   = '[' . ( $Plugins::SqueezeDSP::Plugin::convolver // 'SqueezeDSP' ) . ']';
    my $table = \%Slim::Player::TranscodingHelper::commandTable;
    for my $profile ( keys %$table ) {
        return 1 if $profile =~ /^\Q$in\E-\Q$out\E-.+-\Q$id\E(?:-\d+)?$/ && index( $table->{$profile} // '', $tag ) >= 0;
    }
    return 0;
}

# Whether LMS has read the playing track's stream to its end: it has moved on to the next track's
# (streamingSong is the queue's first song, playingSong its last: StreamingController.pm:1805-1811),
# or it is in STREAMOUT, which the end of a stream sets (:1893-1898 and the LocalEndOfStream row),
# or it went on from there to IDLE still holding that stream: the player asked for the next stream
# (ReadyToStream: _RetryOrNext, _NextIfMore, :912-939 and :1003-1013, set IDLE first) and the queue
# had none, so nothing replaced it. Every other way to IDLE drops the stream first (_pauseStreaming
# :463-468, a paused stream closed before its end; _Stop :612-615; _Stream's reopen :1245-1248), and
# so does every other way to a new song. A radio re-stream waiting on its handler (_RetryOrNext into
# _Stream's updateOnStream, :1172-1190) still holds the old stream, but in TRACKWAIT, not IDLE.
sub _sent {
    my ( $controller, $playing, $streaming ) = @_;
    return 0 unless ref $streaming;
    return 1 if refaddr($streaming) != refaddr($playing);
    return 1 if eval { $controller->isStreamout };
    my $stream = eval { $controller->songStreamController };
    my $song   = ref $stream ? eval { $stream->song } : undef;
    return 0 unless ref $song && refaddr($song) == refaddr($playing);
    my $state = eval { $controller->_getStreamingState };
    my $idle  = eval { Slim::Player::StreamingController::IDLE() };
    return ( defined $state && defined $idle && $state == $idle ) ? 1 : 0;
}

# The page's signal-path card for one player
sub report {
    my $client = shift;
    my $id = $client->id;
    my $now = _now($client);
    my $playing   = eval { $client->playingSong };
    my $streaming = eval { $client->streamingSong };
    my $controller = eval { $client->controller };
    my $stream     = eval { $controller->songStreamController };

    # LMS already streams the next track (gapless): the pipeline running is that track's
    my $next;
    if ( ref $playing && ref $streaming && refaddr($playing) != refaddr($streaming) ) {
        my $index = eval { $streaming->index };
        if ( defined $index ) {
            $next = _now( $client, $index );
            delete $next->{mode};
        }
    }

    # whether LMS holds a stream for the player: only its controller can say (every LMS client has one, Client.pm:312)
    my $m = measure( $id, ( $next || $now )->{rate}, ref $controller ? ( ref $stream ? 1 : 0 ) : undef, sub { _dsdOut( $client, @_ ) } );
    _dsdSettings( $client, $m->{resampler} ) if ( ( $m->{resampler} || {} )->{tool} // '' ) eq 'decoder';
    $m->{bypassed} = 1 if $m->{running} && eval { _bypassed( $client, $m ) };
    if ( $m->{running} ) {
        # the engine's fill, or the player's last one, named by its track (a gapless change never runs flat out)
        if ( defined $m->{realtime} ) {
            $fill{$id} = { speed => $m->{realtime}, track => ( $next || $now )->{title} };
        }
        elsif ( my $f = $fill{$id} ) {
            $m->{realtime}      = $f->{speed};
            $m->{realtime_from} = $f->{track} // '';
        }
        $last{$id} = { stream => $stream, m => {%$m} };
        weaken( $last{$id}{stream} ) if ref $stream;
        $m->{next} = $next if $next;
    }
    elsif ( ref $playing && $now->{mode} ne 'stop' && _sent( $controller, $playing, $streaming ) ) {
        # no pipeline, a track playing or paused (a stopped player keeps its last song) whose stream
        # LMS has read to its end: if that stream went through us, the whole track is with the player.
        # Its figures only while LMS still holds the very stream they were taken from.
        my $mem  = $last{$id};
        my $song = ref $stream ? eval { $stream->song } : undef;
        if ( $mem && ref $mem->{stream} && ref $stream && refaddr( $mem->{stream} ) == refaddr($stream)
             && ref $song && refaddr($song) == refaddr($playing) ) {
            $m = { %{ $mem->{m} }, running => 0, finished => 1 };
        }
        elsif ( _ourRule( $client, $playing ) ) {
            $m->{finished} = 1;
        }
    }
    return { now => $now, pipeline => $m };
}

# A pipeline with no resampler although Upsample is on for the player, because its track is already at its
# target: the stream start gave it the rule the player has with Upsample off (Upsample::_tokenize). Judged by
# the same rule (Upsample::pick) from the rate the engine reads, so a pipeline started before Upsample was
# switched on, at another rate, is not called bypassed. A DSD track never is: its own rule always takes it
# to the target (Configuration.pm dxdTarget), so the engine's rate is the target whatever the resampler.
# The targets are those of the pipeline's own output: a PCM-mode player's FLAC rules stop at FLAC's cap.
# A reduced stream (MP3, Opus, Ogg) is never upsampled, so never bypassed either.
sub _bypassed {
    my ( $client, $m ) = @_;
    return 0 if $m->{resampler};
    return 0 if ( $m->{output} || {} )->{reduced};
    return 0 if grep { ( $_->{role} // '' ) eq 'dsd' } @{ $m->{stages} || [] };
    my $rate = ( $m->{dsp} || {} )->{rate};
    return 0 unless defined $rate && $rate =~ /^\d+$/ && $rate > 0;
    my $spec = Plugins::SqueezeDSP::Configuration::upsampleSpec( $client, Plugins::SqueezeDSP::Configuration::clientPrefs($client),
        ( $m->{output} || {} )->{format} ) or return 0;
    return Plugins::SqueezeDSP::Upsample::pick( $rate, $spec->{f44}, $spec->{f48} ) == $rate ? 1 : 0;
}

# Our engine's DSD decode: its low-pass and gain as the engine takes them, the player's DSDCutoff and DSDGain (read
# at each track: LyrionDSPSettings buildConfig) where its command line gives none; an out-of-range or unreadable
# value is the engine's own default instead (30 kHz, 0 dB), as the engine does
sub _dsdSettings {
    my ( $client, $r ) = @_;
    my $p = eval { Plugins::SqueezeDSP::Configuration::clientPrefs($client) } || {};
    my $num = sub { my $v = shift; ( defined $v && $v =~ /^\s*(-?\d+(?:\.\d+)?)\s*$/ ) ? 0 + $1 : undef };
    unless ( defined $r->{cutoff} ) {
        my $c = $num->( $p->{DSDCutoff} );
        $r->{cutoff} = ( defined $c && ( $c == 0 || ( $c > 20000 && $c <= 60000 ) ) ) ? $c : 30000;
    }
    unless ( defined $r->{gain} ) {
        my $g = $num->( $p->{DSDGain} );
        $r->{gain} = ( defined $g && $g >= -24 && $g <= 12 ) ? $g : 0;
    }
    # native: the integrator the engine runs, the player's DSDIntegrator (lin, int, min or nonring, quotes and spaces
    # trimmed, any case), else lin; its phase as the converter's filters name theirs (non-ringing is linear: symmetric)
    if ( $r->{native} ) {
        my $i = lc( ( $p->{DSDIntegrator} // '' ) =~ s/^[ "]+|[ "]+$//gr );
        $r->{integrator} = $i =~ /^(?:lin|int|min|nonring)$/ ? $i : 'lin';
        $r->{phase} = { int => 'intermediate', min => 'minimum' }->{ $r->{integrator} } // 'linear';
    }
    return $r;
}

# DSF out as the engine takes it at each track (LyrionDSPProcessAudio, DSF out): the rate its --dsdrate (a DSD rate,
# or source), else the player's DSDOutRate (one of the four DSD rates, a number or its string), else the source's own
# (LMS records a DSF's rate as its DSD rate); then capped by its --dsdmax, else the player's DSDOutMax (one of the
# four). Our rules give both flags (TemplateConfig template_DSF: source, DSD512; template_DSFUP: the converter's rate
# twice). The modulator: DSDOutMod (standard, clans or adaptive, quotes and spaces trimmed, any case), else standard
# (LyrionDSPSettings buildConfig). $args: the engine's arguments.
my %DSD_RATES = map { $_ => 1 } ( 2822400, 5644800, 11289600, 22579200 );
sub _dsdOut {
    my ( $client, $srcRate, $args ) = @_;
    $args ||= {};
    my $p = eval { Plugins::SqueezeDSP::Configuration::clientPrefs($client) } || {};
    my $num = sub { my $v = shift; ( defined $v && $v =~ /^\s*(\d+)(?:\.0*)?\s*$/ ) ? 0 + $1 : undef };
    my $flag = $args->{dsdrate};
    my $r = ( defined $flag && lc $flag eq 'source' ) ? undef : $num->( defined $flag ? $flag : $p->{DSDOutRate} );
    my $rate = ( defined $r && $DSD_RATES{$r} ) ? $r : $num->($srcRate);
    my $max = $num->( defined $args->{dsdmax} ? $args->{dsdmax} : $p->{DSDOutMax} );
    $rate = $max if defined $rate && defined $max && $DSD_RATES{$max} && $rate > $max;
    my $mod = lc( ( $p->{DSDOutMod} // '' ) =~ s/^[ "]+|[ "]+$//gr );
    $mod = 'standard' unless $mod =~ /^(?:standard|clans|adaptive)$/;
    return { rate => $rate, mod => $mod };
}

# squeezedsp.status: the page polls it, one request at a time, while it is open
sub statusQuery {
    my $request = shift;
    my $client = $request->client();
    unless ($client) {
        $request->setStatusBadParams();
        return;
    }
    my $r = eval { report($client) };
    $request->addResult( 'status', $r || { error => 1 } );
    $request->setStatusDone();
}

1;
