"""Train a small CNN on a dataset the node staged at /data, and save the model.

This is the job ClearGate exists to run: the renter supplies this script and a
dataset URL, the node downloads the data and runs this on its GPU, and the
renter gets back /out/model.pt.

Two things the container cannot do, by design:
  * reach the network — it runs with no interfaces at all, so this script never
    downloads anything. Everything it needs is already in /data.
  * write outside /work, /out and /tmp — the root filesystem is read-only.

Run it with:
  cleargate run -n <node> -i pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime \\
    --gpu -s examples/train_mnist.py -d <dataset-url> -o model.tar
"""

import json
import os
import pathlib
import sys
import time

import torch
import torch.nn as nn
import torch.nn.functional as F
from torch.utils.data import DataLoader, TensorDataset

DATA = pathlib.Path("/data")
OUT = pathlib.Path("/out")

# The card this was written for is a 4 GB RTX 3050. Keep the batch small enough
# that a laptop GPU is not the reason a paid job dies.
BATCH_SIZE = int(os.environ.get("BATCH_SIZE", "128"))
EPOCHS = int(os.environ.get("EPOCHS", "3"))
LEARNING_RATE = float(os.environ.get("LEARNING_RATE", "0.01"))


def describe_device() -> torch.device:
    """Print exactly what we are training on, so nobody has to guess."""
    if torch.cuda.is_available():
        device = torch.device("cuda")
        name = torch.cuda.get_device_name(0)
        total = torch.cuda.get_device_properties(0).total_memory / (1024**3)
        print(f"device    cuda — {name} ({total:.1f} GiB)", flush=True)
        print(f"torch     {torch.__version__}  cuda {torch.version.cuda}", flush=True)
    else:
        device = torch.device("cpu")
        print("device    cpu — no CUDA available in this container", flush=True)
        print("          if you paid for a GPU node, that is worth complaining about", flush=True)
    return device


def load_dataset() -> tuple[TensorDataset, TensorDataset, int]:
    """Load whatever the node staged at /data.

    Accepts an MNIST-style .npz with x_train/y_train/x_test/y_test, or falls
    back to a synthetic set so the job still demonstrates the full path when the
    renter supplies no dataset at all.
    """
    files = sorted(p for p in DATA.rglob("*") if p.is_file())
    print(f"dataset   {len(files)} file(s) staged at /data", flush=True)
    for path in files[:10]:
        print(f"          {path.relative_to(DATA)}  {path.stat().st_size} bytes", flush=True)
    if len(files) > 10:
        print(f"          … and {len(files) - 10} more", flush=True)

    npz = next((p for p in files if p.suffix == ".npz"), None)
    if npz is not None:
        import numpy as np

        print(f"loading   {npz.name}", flush=True)
        with np.load(npz) as raw:
            keys = set(raw.files)
            if not {"x_train", "y_train"} <= keys:
                raise SystemExit(
                    f"{npz.name} has keys {sorted(keys)}; expected at least x_train and y_train"
                )
            x_train = torch.tensor(raw["x_train"], dtype=torch.float32) / 255.0
            y_train = torch.tensor(raw["y_train"], dtype=torch.long)
            if "x_test" in keys:
                x_test = torch.tensor(raw["x_test"], dtype=torch.float32) / 255.0
                y_test = torch.tensor(raw["y_test"], dtype=torch.long)
            else:
                split = int(len(x_train) * 0.9)
                x_train, x_test = x_train[:split], x_train[split:]
                y_train, y_test = y_train[:split], y_train[split:]
    else:
        print("loading   no .npz found — training on synthetic data instead", flush=True)
        print("          (the payment, staging and artifact path are still exercised)", flush=True)
        generator = torch.Generator().manual_seed(7)
        x_train = torch.rand(6000, 28, 28, generator=generator)
        y_train = (x_train.mean(dim=(1, 2)) * 10).long().clamp(0, 9)
        x_test = torch.rand(1000, 28, 28, generator=generator)
        y_test = (x_test.mean(dim=(1, 2)) * 10).long().clamp(0, 9)

    x_train = x_train.unsqueeze(1)
    x_test = x_test.unsqueeze(1)
    classes = int(max(y_train.max().item(), y_test.max().item())) + 1

    print(f"shapes    train {tuple(x_train.shape)}  test {tuple(x_test.shape)}  classes {classes}", flush=True)
    return TensorDataset(x_train, y_train), TensorDataset(x_test, y_test), classes


class SmallCNN(nn.Module):
    """Deliberately small: it has to fit, and finish, on a 4 GB laptop GPU."""

    def __init__(self, classes: int) -> None:
        super().__init__()
        self.conv1 = nn.Conv2d(1, 16, 3, padding=1)
        self.conv2 = nn.Conv2d(16, 32, 3, padding=1)
        self.fc1 = nn.Linear(32 * 7 * 7, 128)
        self.fc2 = nn.Linear(128, classes)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        x = F.max_pool2d(F.relu(self.conv1(x)), 2)
        x = F.max_pool2d(F.relu(self.conv2(x)), 2)
        x = x.flatten(1)
        return self.fc2(F.relu(self.fc1(x)))


def accuracy(model: nn.Module, loader: DataLoader, device: torch.device) -> float:
    model.eval()
    correct = total = 0
    with torch.no_grad():
        for images, labels in loader:
            images, labels = images.to(device), labels.to(device)
            correct += (model(images).argmax(dim=1) == labels).sum().item()
            total += labels.numel()
    return correct / max(total, 1)


def main() -> None:
    started = time.time()
    device = describe_device()
    train_set, test_set, classes = load_dataset()

    train_loader = DataLoader(train_set, batch_size=BATCH_SIZE, shuffle=True)
    test_loader = DataLoader(test_set, batch_size=BATCH_SIZE)

    model = SmallCNN(classes).to(device)
    optimizer = torch.optim.SGD(model.parameters(), lr=LEARNING_RATE, momentum=0.9)

    history = []
    for epoch in range(EPOCHS):
        model.train()
        running = 0.0
        for batch, (images, labels) in enumerate(train_loader):
            images, labels = images.to(device), labels.to(device)
            optimizer.zero_grad()
            loss = F.cross_entropy(model(images), labels)
            loss.backward()
            optimizer.step()
            running += loss.item()

            # Print often enough that the renter's stream and the provider's
            # dashboard both show the job is alive.
            if batch % 10 == 0:
                print(f"epoch {epoch + 1}/{EPOCHS}  batch {batch:4d}  loss {loss.item():.4f}", flush=True)

        acc = accuracy(model, test_loader, device)
        mean_loss = running / max(len(train_loader), 1)
        history.append({"epoch": epoch + 1, "loss": mean_loss, "accuracy": acc})
        print(f"epoch {epoch + 1}/{EPOCHS}  mean loss {mean_loss:.4f}  accuracy {acc:.4f}", flush=True)

    OUT.mkdir(parents=True, exist_ok=True)
    torch.save(model.state_dict(), OUT / "model.pt")
    (OUT / "metrics.json").write_text(
        json.dumps(
            {
                "device": str(device),
                "gpu": torch.cuda.get_device_name(0) if torch.cuda.is_available() else None,
                "epochs": EPOCHS,
                "batch_size": BATCH_SIZE,
                "classes": classes,
                "history": history,
                "seconds": round(time.time() - started, 2),
            },
            indent=2,
        )
    )
    size = (OUT / "model.pt").stat().st_size
    print(f"\nwrote /out/model.pt ({size} bytes) and /out/metrics.json", flush=True)
    print(f"done in {time.time() - started:.1f}s", flush=True)


if __name__ == "__main__":
    try:
        main()
    except torch.cuda.OutOfMemoryError:
        # A 4 GB card runs out easily. Say so usefully rather than dumping a
        # stack trace on someone who just paid for this.
        print(
            "\nCUDA out of memory. This node's GPU cannot hold a batch this large.\n"
            f"Retry with a smaller batch: --env BATCH_SIZE={max(BATCH_SIZE // 4, 8)}",
            file=sys.stderr,
            flush=True,
        )
        raise SystemExit(1)
